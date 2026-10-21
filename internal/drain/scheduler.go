package drain

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bmena34/capataz/internal/issues"
)

// Drain runs the hands-off drain loop for cfg.Feature until no dispatchable
// issues remain. It uses runner for all external shell-outs so the loop is
// fully testable without real processes.
//
// Loop:
//  1. Call `capataz issue waves <feature> --json` to get the current dispatchable set.
//  2. Take up to cfg.Concurrency issues from wave 0.
//  3. For each issue in parallel:
//     a. claim  → b. worktree create → c. agent start → d. agent wait → e. resolve on done.
//  4. Re-scan. Stop when waves is empty.
func Drain(cfg Config, runner Runner) error {
	// logf is called from concurrent dispatchOne goroutines, so serialize writes
	// to the shared cfg.Out (else lines interleave / the strings.Builder races).
	var logMu sync.Mutex
	logf := func(format string, args ...any) {
		logMu.Lock()
		fmt.Fprintf(cfg.Out, format+"\n", args...)
		logMu.Unlock()
	}

	if cfg.AgentTimeoutMS <= 0 {
		cfg.AgentTimeoutMS = 3_600_000 // 1 hour default
	}

	logf("drain: feature=%s concurrency=%d skip_permissions=%v dry_run=%v",
		cfg.Feature, cfg.Concurrency, cfg.SkipPermissions, cfg.DryRun)

	// failed tracks issue paths that failed this run so a permanently-failing
	// issue (still ready-for-agent because capataz-work never claimed it) is not
	// re-dispatched forever. maxRounds is a hard backstop.
	const maxRounds = 100
	failed := map[string]int{}
	// mergeBack mutates the shared base repo (git merge, possibly a whole
	// conflict-resolution session). Serialize it so concurrent dispatches don't
	// race on git's index lock or step on each other's in-progress merge.
	var mergeMu sync.Mutex

	for round := 1; ; round++ {
		if round > maxRounds {
			return fmt.Errorf("drain stopped: exceeded %d rounds (possible loop)", maxRounds)
		}

		raw, err := runner.Waves(cfg.Feature, cfg.RepoPath)
		if err != nil {
			return fmt.Errorf("round %d: waves: %w", round, err)
		}
		waves, err := parseWaves(raw)
		if err != nil {
			return fmt.Errorf("round %d: parse waves: %w", round, err)
		}

		batch := dispatchable(waves.Waves, cfg.Concurrency)
		if len(batch) == 0 {
			logf("drain: no dispatchable issues — done")
			return nil
		}

		// Skip issues that already failed this run — otherwise a still-ready
		// failing issue would spin the loop (worktree-create collides forever).
		var todo []WaveIssue
		for _, iss := range batch {
			if failed[iss.Path] == 0 {
				todo = append(todo, iss)
			}
		}
		if len(todo) == 0 {
			logf("drain: no progress — %d dispatchable issue(s) already failed; stopping", len(batch))
			return fmt.Errorf("drain stopped: %d issue(s) failed and remain undispatched", len(failed))
		}

		if cfg.DryRun {
			logf("drain: round %d — dispatching %d issue(s) (dry run)", round, len(todo))
			printDryRun(cfg, todo, round)
			return nil
		}

		logf("drain: round %d — %d dispatchable, dispatching %d", round, len(batch), len(todo))
		results := dispatchBatch(cfg, runner, todo, logf, &mergeMu)

		// Record outcomes. done issues drop from the next waves scan (capataz-work
		// resolved them); anything else is recorded as failed so it isn't retried.
		for _, res := range results {
			switch res.Status {
			case "done":
				logf("drain: [%s] done", res.Issue.Number)
			case "blocked":
				failed[res.Issue.Path]++
				logf("drain: [%s] BLOCKED — operator attention required (path: %s)",
					res.Issue.Number, res.Issue.Path)
			case "timeout":
				failed[res.Issue.Path]++
				logf("drain: [%s] TIMEOUT after %dms — operator attention required (path: %s)",
					res.Issue.Number, cfg.AgentTimeoutMS, res.Issue.Path)
			default:
				failed[res.Issue.Path]++
				logf("drain: [%s] ERROR: %v", res.Issue.Number, res.Err)
			}
		}
	}
}

// dispatchBatch claims, creates worktrees, starts agents, and waits for all
// issues in batch concurrently. It returns one IssueResult per issue.
func dispatchBatch(cfg Config, runner Runner, batch []WaveIssue, logf func(string, ...any), mergeMu *sync.Mutex) []IssueResult {
	results := make([]IssueResult, len(batch))
	var wg sync.WaitGroup
	for i, iss := range batch {
		wg.Add(1)
		go func(idx int, issue WaveIssue) {
			defer wg.Done()
			results[idx] = dispatchOne(cfg, runner, issue, logf, mergeMu)
		}(i, iss)
	}
	wg.Wait()
	return results
}

// startAgentWithTask launches a claude agent in paneID and reliably submits the
// task prompt. A fresh claude can drop a prompt submitted before it's ready and
// sit idle, so we confirm the agent actually left idle (AgentReachedWorking) and
// resubmit the prompt if not. Returns an error if the agent never starts.
func startAgentWithTask(runner Runner, name, paneID, prompt string, skipPerms bool, num string, logf func(string, ...any)) error {
	logf("drain: [%s] starting agent %s in pane %s", num, name, paneID)
	if err := runner.AgentStart(name, paneID, skipPerms); err != nil {
		return fmt.Errorf("agent start: %w", err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		logf("drain: [%s] submitting task to agent %s (attempt %d)", num, name, attempt)
		if err := runner.AgentPrompt(name, prompt); err != nil {
			return fmt.Errorf("agent prompt: %w", err)
		}
		if runner.AgentReachedWorking(name) {
			return nil
		}
		logf("drain: [%s] agent %s still idle after prompt — retrying", num, name)
	}
	return fmt.Errorf("agent %s never started working after 3 prompt attempts", name)
}

// dispatchOne handles a single issue: worktree → agent (capataz-work) → wait →
// verify commits → merge → cleanup. It does NOT claim or resolve — capataz-work
// owns claim → implement → resolve (and refuses a pre-claimed issue). The drain
// only verifies the agent actually produced commits, then merges.
func dispatchOne(cfg Config, runner Runner, iss WaveIssue, logf func(string, ...any), mergeMu *sync.Mutex) IssueResult {
	if cfg.askPoll <= 0 {
		cfg.askPoll = 15 * time.Second
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.sleep == nil {
		cfg.sleep = time.Sleep
	}
	if cfg.stallPoll <= 0 {
		cfg.stallPoll = stallPoll
	}
	if cfg.walk == nil {
		cfg.walk = newestMtime
	}
	fail := func(status string, err error) IssueResult {
		return IssueResult{Issue: iss, Status: status, Err: err}
	}

	// Step a: create a herdr worktree for this branch. NOTE: we do not claim the
	// issue — capataz-work claims it itself and stops if it's already claimed, so
	// pre-claiming here would make the agent refuse to do the work.
	logf("drain: [%s] creating worktree branch=%s", iss.Number, iss.Branch)
	workspaceID, checkoutPath, err := runner.WorktreeCreate(iss.Branch, cfg.RepoPath)
	if err != nil {
		return fail("error", fmt.Errorf("worktree create: %w", err))
	}

	// From here a hard failure must tear down the worktree + branch, so a re-scan
	// doesn't collide with a half-created worktree. (blocked/timeout are NOT hard
	// failures — those leave the worktree in place for the operator to attend.)
	failClean := func(status string, err error) IssueResult {
		logf("drain: [%s] cleaning up worktree %s after failure", iss.Number, workspaceID)
		if e := runner.WorktreeRemove(workspaceID); e != nil {
			logf("drain: [%s] worktree remove failed: %v", iss.Number, e)
		}
		if e := runner.DeleteBranch(cfg.RepoPath, iss.Branch); e != nil {
			logf("drain: [%s] branch delete failed: %v", iss.Number, e)
		}
		return fail(status, err)
	}

	agentName := fmt.Sprintf("drain-%s-%s", cfg.Feature, iss.Number)
	var paneID string

	// After the agent has run, the worktree may hold work that exists nowhere
	// else (uncommitted edits, unmerged commits, an open question, or a question
	// asked in chat). failSafe never tears that down: it returns a blocked result
	// and keeps worktree + branch. Only a clean worktree with no commits ahead,
	// on an issue the agent never claimed, falls through to failClean.
	failSafe := func(status string, cause error) IssueResult {
		dirty, derr := runner.WorktreeDirty(checkoutPath)
		if derr != nil {
			logf("drain: [%s] cannot inspect worktree %s (%v) — keeping it", iss.Number, checkoutPath, derr)
			return fail("error", fmt.Errorf("%w (worktree %s kept: dirty check failed: %v)", cause, checkoutPath, derr))
		}
		st, serr := runner.IssueStatus(iss.Path)
		if serr != nil {
			logf("drain: [%s] cannot read issue status (%v) — keeping worktree %s", iss.Number, serr, checkoutPath)
			return fail("error", fmt.Errorf("%w (worktree %s kept: status read failed: %v)", cause, checkoutPath, serr))
		}
		st = normStatus(st)
		switch {
		case st == issues.StatusNeedsInfo || st == issues.StatusReadyForHuman || st == issues.StatusAsking:
			logf("drain: [%s] issue is %s — it needs an answer; keeping worktree %s and branch %s", iss.Number, st, checkoutPath, iss.Branch)
			return fail("blocked", fmt.Errorf("issue is %s and needs an answer; worktree kept at %s", st, checkoutPath))
		case dirty:
			logf("drain: [%s] worktree %s has uncommitted or untracked changes — keeping it and branch %s", iss.Number, checkoutPath, iss.Branch)
			return fail("blocked", fmt.Errorf("worktree has uncommitted changes (%v); kept at %s", cause, checkoutPath))
		case st == issues.StatusClaimed:
			logf("drain: [%s] agent %s (pane %s) ended with the issue still claimed — it may have asked in chat; keeping worktree %s and branch %s", iss.Number, agentName, paneID, checkoutPath, iss.Branch)
			title := fmt.Sprintf("capataz: %s %s stopped while claimed", cfg.Feature, iss.Number)
			if err := runner.Notify(title, fmt.Sprintf("Agent %s may have asked in chat — check pane %s.", agentName, paneID)); err != nil {
				logf("drain: [%s] notification failed: %v", iss.Number, err)
			}
			return fail("blocked", fmt.Errorf("issue still claimed (%v); agent may have asked in chat — pane %s, worktree kept at %s", cause, paneID, checkoutPath))
		}
		ahead, aerr := runner.CommitsAhead(cfg.RepoPath, iss.Branch)
		if aerr != nil {
			logf("drain: [%s] cannot count commits on %s (%v) — keeping worktree %s and branch", iss.Number, iss.Branch, aerr, checkoutPath)
			return fail("blocked", fmt.Errorf("%w (branch %s kept: commit check failed: %v; worktree at %s)", cause, iss.Branch, aerr, checkoutPath))
		}
		if ahead > 0 {
			logf("drain: [%s] branch %s has %d unmerged commit(s) — keeping it and worktree %s", iss.Number, iss.Branch, ahead, checkoutPath)
			return fail("blocked", fmt.Errorf("%w (branch %s has %d unmerged commit(s); kept, worktree at %s)", cause, iss.Branch, ahead, checkoutPath))
		}
		return failClean(status, cause)
	}

	// Step b: pre-seed Claude folder-trust for the fresh worktree dir, else the
	// agent hangs at the "trust this folder?" prompt (which
	// --dangerously-skip-permissions does not bypass) and does no work.
	logf("drain: [%s] trusting worktree dir %s", iss.Number, checkoutPath)
	if err := runner.TrustDir(checkoutPath); err != nil {
		return failClean("error", fmt.Errorf("trust dir: %w", err))
	}

	// Resolve the pane ID in the new workspace.
	paneID, err = runner.PaneList(workspaceID)
	if err != nil {
		return failClean("error", fmt.Errorf("pane list workspace=%s: %w", workspaceID, err))
	}

	// Step c: start a Claude agent running /capataz-work in the worktree pane and
	// reliably deliver the task (retrying if a fresh claude drops the first prompt).
	// capataz-work claims, implements, commits on the branch, and resolves.
	prompt := workPrompt(iss)
	if err := startAgentWithTask(runner, agentName, paneID, prompt, cfg.SkipPermissions, iss.Number, logf); err != nil {
		return failSafe("error", err)
	}

	// Step d: wait for the agent on the unfocused pane. A blocked agent is
	// waited out (see waitAgent); a timeout leaves the worktree for the operator
	// and does not merge. An agent that ended its turn with the issue `asking` is
	// waiting on the operator: wait for the answer, deliver it, and wait on the
	// agent again. Time spent blocked or asking does not count against the timeout
	// or toward a stall alert (see stallWatch).
	budget := time.Duration(cfg.AgentTimeoutMS) * time.Millisecond
	stall := newStallWatch(cfg, runner, iss, agentName, paneID, checkoutPath)
	for {
		var status string
		status, budget, err = waitAgent(cfg, runner, agentName, paneID, fmt.Sprintf("%s %s", cfg.Feature, iss.Number), iss.Number, budget, stall, logf)
		if err != nil {
			return failSafe("error", fmt.Errorf("agent wait: %w", err))
		}
		if status != "done" {
			return fail(status, nil) // leave the worktree for the operator to inspect
		}
		delivered, res := awaitAnswer(cfg, runner, iss, agentName, logf)
		if res != nil {
			return failSafe(res.status, res.err)
		}
		if !delivered {
			break
		}
		stall.rearm()
	}

	// Step e: verify the agent actually produced work. capataz-work commits on the
	// branch; if the branch has no commits ahead of base, the agent did nothing
	// (e.g. it refused an already-claimed issue, or errored). An empty branch would
	// otherwise merge as a no-op ("already up to date") and look like success, so
	// never merge zero commits; failSafe decides whether to keep or clean up.
	ahead, err := runner.CommitsAhead(cfg.RepoPath, iss.Branch)
	if err != nil {
		return failSafe("error", fmt.Errorf("verify commits on %s: %w", iss.Branch, err))
	}
	if ahead == 0 {
		return failSafe("error", fmt.Errorf("agent produced no commits on %s — capataz-work did not implement the issue", iss.Branch))
	}
	logf("drain: [%s] verified %d commit(s) on %s", iss.Number, ahead, iss.Branch)

	// Step e: merge the worktree branch back into the base repo, then remove the
	// worktree. Conflict → run a fix-conflict agent first. Serialized across
	// concurrent dispatches so only one merge-back touches the base repo at a time.
	logf("drain: [%s] merging branch %s into base %s", iss.Number, iss.Branch, cfg.RepoPath)
	mergeMu.Lock()
	res := mergeBack(cfg, runner, iss, workspaceID, logf)
	mergeMu.Unlock()
	if res != nil {
		return fail("error", res)
	}

	return IssueResult{Issue: iss, Status: "done"}
}

// unblockedPoll bounds each `herdr agent wait` while an agent sits blocked;
// the drain re-waits until it leaves blocked, however long that takes.
const unblockedPoll = time.Hour

// waitAgent waits up to budget for agent name (in paneID) to end its turn and
// returns "done", "timeout", or another herdr status, plus the budget left.
// A blocked agent (usually a permission prompt) is not abandoned: the operator
// is notified once, the drain waits for the agent to leave blocked, and that
// time does not count against budget. A timeout notifies too. who names the
// work in notifications ("<feature> NN"). A non-nil stall is checked every
// cfg.stallPoll while the agent works. An agent that disappears from herdr
// ends the wait with an error.
func waitAgent(cfg Config, runner Runner, name, paneID, who, num string, budget time.Duration, stall *stallWatch, logf func(string, ...any)) (string, time.Duration, error) {
	notified := false
	logged := false
	for {
		if budget <= 0 {
			return timedOut(runner, name, paneID, who, num, logf), budget, nil
		}
		if !logged {
			logged = true
			logf("drain: [%s] waiting for agent %s (timeout=%dms)", num, name, budget.Milliseconds())
		}
		slice := budget
		if stall != nil && slice > cfg.stallPoll {
			slice = cfg.stallPoll
		}
		tick := slice < budget // this wait ends at a stall poll, not the outer timeout
		start := cfg.now()
		status, err := runner.AgentWait(name, int(slice.Milliseconds()))
		budget -= cfg.now().Sub(start)
		if err != nil {
			return "error", budget, err
		}
		switch status {
		case statusGone:
			return "error", budget, agentGone(name, paneID, num, logf)
		case "done", "idle":
			return "done", budget, nil
		case "timeout":
			if tick {
				stall.check(logf)
				continue
			}
			return timedOut(runner, name, paneID, who, num, logf), budget, nil
		case "blocked":
		default:
			return status, budget, nil
		}

		logf("drain: [%s] BLOCKED — agent %s in pane %s is waiting on a prompt; waiting for it to resume", num, name, paneID)
		if !notified {
			notified = true
			title := fmt.Sprintf("capataz: %s is waiting on a prompt", who)
			if err := runner.Notify(title, fmt.Sprintf("Agent %s needs input — check pane %s.", name, paneID)); err != nil {
				logf("drain: [%s] notification failed: %v", num, err)
			}
		}
		for {
			st, err := runner.AgentUnblocked(name, int(unblockedPoll.Milliseconds()))
			if err != nil {
				return "error", budget, fmt.Errorf("wait for %s to leave blocked: %w", name, err)
			}
			if st == statusGone {
				return "error", budget, agentGone(name, paneID, num, logf)
			}
			if st != "blocked" && st != "timeout" {
				break
			}
		}
		logf("drain: [%s] agent %s resumed", num, name)
		logged = false
		if stall != nil {
			stall.rearm()
		}
	}
}

// agentGone logs and returns the error for an agent herdr no longer knows: its
// pane was closed or it crashed, so there is nothing left to wait for.
func agentGone(name, paneID, num string, logf func(string, ...any)) error {
	logf("drain: [%s] agent %s is gone — its pane %s was closed or it exited", num, name, paneID)
	return fmt.Errorf("agent %s is gone (pane %s closed or the agent exited)", name, paneID)
}

// timedOut notifies the operator that name ran out of time and returns "timeout".
func timedOut(runner Runner, name, paneID, who, num string, logf func(string, ...any)) string {
	logf("drain: [%s] agent %s timed out — keeping its worktree (pane %s)", num, name, paneID)
	title := fmt.Sprintf("capataz: %s timed out", who)
	if err := runner.Notify(title, fmt.Sprintf("Agent %s ran out of time — check pane %s.", name, paneID)); err != nil {
		logf("drain: [%s] notification failed: %v", num, err)
	}
	return "timeout"
}

// askFailure is an awaitAnswer outcome that ends the dispatch via failSafe.
type askFailure struct {
	status string
	err    error
}

// awaitAnswer handles an agent that ended its turn with its issue `asking`:
// it notifies the operator, polls the issue until it leaves `asking`, and, if
// the operator answered (back to `claimed`), prompts the same agent with the
// answer. delivered=false means the issue was not asking — carry on to commit
// verification. Any other status the issue moves to ends the dispatch.
func awaitAnswer(cfg Config, runner Runner, iss WaveIssue, agentName string, logf func(string, ...any)) (delivered bool, fail *askFailure) {
	st, err := runner.IssueStatus(iss.Path)
	if err != nil {
		logf("drain: [%s] cannot read issue status (%v) — assuming it is not asking", iss.Number, err)
		return false, nil
	}
	if normStatus(st) != issues.StatusAsking {
		return false, nil
	}

	question, err := runner.IssueQuestion(iss.Path)
	if err != nil {
		logf("drain: [%s] cannot read the question (%v)", iss.Number, err)
	}
	logf("drain: [%s] QUESTION: %s", iss.Number, question)
	logf("drain: [%s] answer with: capataz issue answer %s \"…\"", iss.Number, iss.Path)
	title := fmt.Sprintf("capataz: %s %s has a question", cfg.Feature, iss.Number)
	if err := runner.Notify(title, question); err != nil {
		logf("drain: [%s] notification failed: %v", iss.Number, err)
	}

	for normStatus(st) == issues.StatusAsking {
		cfg.sleep(cfg.askPoll)
		if st, err = runner.IssueStatus(iss.Path); err != nil {
			logf("drain: [%s] cannot read issue status (%v) — still waiting", iss.Number, err)
			st = issues.StatusAsking
		}
	}
	st = normStatus(st)
	if st != issues.StatusClaimed {
		logf("drain: [%s] issue moved from asking to %s — no longer waiting", iss.Number, st)
		return false, &askFailure{"blocked", fmt.Errorf("issue moved from asking to %s", st)}
	}

	answer, err := runner.IssueAnswer(iss.Path)
	if err != nil {
		return false, &askFailure{"error", fmt.Errorf("read answer: %w", err)}
	}
	logf("drain: [%s] delivering answer to agent %s", iss.Number, agentName)
	prompt := fmt.Sprintf("Your question on %s was answered: %s. Continue the issue.", iss.Path, answer)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := runner.AgentPrompt(agentName, prompt); err != nil {
			return false, &askFailure{"error", fmt.Errorf("deliver answer: %w", err)}
		}
		if runner.AgentReachedWorking(agentName) {
			return true, nil
		}
		logf("drain: [%s] agent %s still idle after answer — retrying", iss.Number, agentName)
	}
	return false, &askFailure{"error", fmt.Errorf("agent %s never resumed after the answer (3 attempts)", agentName)}
}

func normStatus(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// printDryRun prints the planned dispatch for the first wave without executing
// anything. It is the safe manual-verification path.
func printDryRun(cfg Config, batch []WaveIssue, round int) {
	fmt.Fprintf(cfg.Out, "\n=== DRY RUN — feature: %s (round %d) ===\n", cfg.Feature, round)
	fmt.Fprintf(cfg.Out, "concurrency cap : %d (0 = unlimited)\n", cfg.Concurrency)
	fmt.Fprintf(cfg.Out, "skip_permissions: %v\n", cfg.SkipPermissions)
	fmt.Fprintf(cfg.Out, "agent timeout   : %dms\n", cfg.AgentTimeoutMS)
	fmt.Fprintf(cfg.Out, "repo path       : %s\n\n", cfg.RepoPath)
	fmt.Fprintf(cfg.Out, "Would dispatch %d issue(s):\n\n", len(batch))
	for i, iss := range batch {
		agentName := fmt.Sprintf("drain-%s-%s", cfg.Feature, iss.Number)
		prompt := workPrompt(iss)
		posture := "--allowedTools " + allowedTools
		if cfg.SkipPermissions {
			posture = "--dangerously-skip-permissions"
		}
		fmt.Fprintf(cfg.Out, "  [%d] issue %s — %s\n", i+1, iss.Number, iss.Path)
		fmt.Fprintf(cfg.Out, "      branch : %s\n", iss.Branch)
		fmt.Fprintf(cfg.Out, "      cmd[1] : herdr worktree create --no-focus --branch %s --cwd %s\n",
			iss.Branch, cfg.RepoPath)
		fmt.Fprintf(cfg.Out, "      cmd[2] : capataz trust-dir <worktree-checkout-path>\n")
		fmt.Fprintf(cfg.Out, "      cmd[3] : herdr agent start %s --kind claude --pane <pane-id> -- %s\n",
			agentName, posture)
		fmt.Fprintf(cfg.Out, "      cmd[4] : herdr agent prompt %s %q\n", agentName, prompt)
		fmt.Fprintf(cfg.Out, "               (capataz-work claims → implements → commits → resolves)\n")
		fmt.Fprintf(cfg.Out, "      cmd[5] : herdr agent wait %s --until done --until blocked --timeout %d\n",
			agentName, cfg.AgentTimeoutMS)
		fmt.Fprintf(cfg.Out, "               (blocked → herdr notification show; wait --until working|done|idle, then wait again)\n")
		fmt.Fprintf(cfg.Out, "               (issue asking → herdr notification show; wait for\n")
		fmt.Fprintf(cfg.Out, "                capataz issue answer %s \"…\", then prompt the answer and wait again)\n", iss.Path)
		fmt.Fprintf(cfg.Out, "      cmd[6] : git -C %s rev-list --count HEAD..%s   (verify >= 1 commit)\n",
			cfg.RepoPath, iss.Branch)
		fmt.Fprintf(cfg.Out, "      cmd[7] : git -C %s merge --no-ff %s\n", cfg.RepoPath, iss.Branch)
		fmt.Fprintf(cfg.Out, "      cmd[8] : herdr worktree remove --workspace <workspace-id>  (clean merge)\n")
		fmt.Fprintf(cfg.Out, "             : OR start fix-conflict agent + wait + remove  (on conflict)\n\n")
	}
	fmt.Fprintf(cfg.Out, "(No worktrees created, no agents started, no merges run.)\n")
}

// workPrompt is the task given to each dispatched agent. It states the
// isolated-worktree contract explicitly because capataz-work only commits on
// its branch when the prompt says so, and the drain fails an issue whose
// branch has no new commits.
func workPrompt(iss WaveIssue) string {
	return fmt.Sprintf("run /capataz-work on the issue at %s. You are in an isolated git worktree on branch %s: "+
		"commit your work on this branch when you resolve, and do not merge or switch branches — the drain merges it back. "+
		"If you need an operator decision, run `capataz issue ask %s \"<question with options>\"` and end your turn; never ask in chat.",
		iss.Path, iss.Branch, iss.Path)
}
