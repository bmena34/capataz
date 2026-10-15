package drain

import (
	"fmt"
	"io"
	"sync"
)

// Config holds all knobs for the drain orchestrator.
type Config struct {
	// Feature is the .scratch feature slug to drain (e.g. "herdr-port").
	Feature string

	// Concurrency is the maximum number of agents running in parallel per wave.
	// 0 means unlimited (take the full wave at once).
	Concurrency int

	// SkipPermissions controls whether dispatched agents run with
	// --dangerously-skip-permissions. Default false: permission prompts surface
	// as herdr "blocked" and the loop surfaces them for operator attention.
	SkipPermissions bool

	// AgentTimeoutMS is the per-agent wait timeout in milliseconds.
	// Default 3600000 (1 hour).
	AgentTimeoutMS int

	// RepoPath is the absolute path to the base repository. Used as --cwd for
	// herdr worktree create so the new worktree is rooted in the correct repo.
	RepoPath string

	// DryRun prints the planned dispatch without creating worktrees or starting
	// agents. Safe manual verification path.
	DryRun bool

	// Out is where drain writes status messages. Defaults to os.Stdout.
	Out io.Writer
}

// IssueResult records the outcome of dispatching a single issue.
type IssueResult struct {
	Issue  WaveIssue
	Status string // "done", "blocked", "timeout", "error"
	Err    error
}

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

	// Step b: pre-seed Claude folder-trust for the fresh worktree dir, else the
	// agent hangs at the "trust this folder?" prompt (which
	// --dangerously-skip-permissions does not bypass) and does no work.
	logf("drain: [%s] trusting worktree dir %s", iss.Number, checkoutPath)
	if err := runner.TrustDir(checkoutPath); err != nil {
		return failClean("error", fmt.Errorf("trust dir: %w", err))
	}

	// Resolve the pane ID in the new workspace.
	paneID, err := runner.PaneList(workspaceID)
	if err != nil {
		return failClean("error", fmt.Errorf("pane list workspace=%s: %w", workspaceID, err))
	}

	// Step c: start a Claude agent running /capataz-work in the worktree pane and
	// reliably deliver the task (retrying if a fresh claude drops the first prompt).
	// capataz-work claims, implements, commits on the branch, and resolves.
	agentName := fmt.Sprintf("drain-%s-%s", cfg.Feature, iss.Number)
	prompt := workPrompt(iss)
	if err := startAgentWithTask(runner, agentName, paneID, prompt, cfg.SkipPermissions, iss.Number, logf); err != nil {
		return failClean("error", err)
	}

	// Step d: wait for the agent on the unfocused pane. blocked/timeout are
	// needs-operator-attention: surface them, leave the worktree, do not merge.
	logf("drain: [%s] waiting for agent %s (timeout=%dms)", iss.Number, agentName, cfg.AgentTimeoutMS)
	status, err := runner.AgentWait(agentName, cfg.AgentTimeoutMS)
	if err != nil {
		return failClean("error", fmt.Errorf("agent wait: %w", err))
	}
	if status != "done" {
		return fail(status, nil) // leave the worktree for the operator to inspect
	}

	// Step e: verify the agent actually produced work. capataz-work commits on the
	// branch; if the branch has no commits ahead of base, the agent did nothing
	// (e.g. it refused an already-claimed issue, or errored). An empty branch would
	// otherwise merge as a no-op ("already up to date") and look like success, so
	// treat zero commits as a failed issue — clean up, don't merge.
	ahead, err := runner.CommitsAhead(cfg.RepoPath, iss.Branch)
	if err != nil {
		return failClean("error", fmt.Errorf("verify commits on %s: %w", iss.Branch, err))
	}
	if ahead == 0 {
		return failClean("error", fmt.Errorf("agent produced no commits on %s — capataz-work did not implement the issue", iss.Branch))
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

// mergeBack merges iss.Branch into cfg.RepoPath and removes the worktree.
// On conflict it starts a fix-conflict agent in the base repo, waits for done,
// then removes the worktree. Returns non-nil only on hard (non-conflict) errors
// or when the fix-conflict agent itself blocks/times out.
func mergeBack(cfg Config, runner Runner, iss WaveIssue, workspaceID string, logf func(string, ...any)) error {
	conflicted, err := runner.Merge(iss.Branch, cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("merge %s: %w", iss.Branch, err)
	}

	if conflicted {
		logf("drain: [%s] CONFLICT merging %s — starting fix-conflict agent in base repo", iss.Number, iss.Branch)

		// Open the base repo as a herdr workspace to get a pane there.
		baseWsID, err := runner.OpenBaseWorkspace(cfg.RepoPath)
		if err != nil {
			return fmt.Errorf("open base workspace for fix-conflict: %w", err)
		}
		basePaneID, err := runner.PaneList(baseWsID)
		if err != nil {
			return fmt.Errorf("pane list base workspace=%s: %w", baseWsID, err)
		}

		// Trust the base repo so the fresh fix-conflict claude doesn't hang on the
		// folder-trust prompt.
		if err := runner.TrustDir(cfg.RepoPath); err != nil {
			return fmt.Errorf("trust base repo for fix-conflict: %w", err)
		}

		// Start a fix-conflict agent in the base repo (where the merge is in
		// progress), reliably delivering its task.
		fixName := fmt.Sprintf("fix-conflict-%s-%s", cfg.Feature, iss.Number)
		fixPrompt := "run /capataz-fix-conflict — resolve the in-progress conflicted merge in this repo and complete it (commit the merge)."
		if err := startAgentWithTask(runner, fixName, basePaneID, fixPrompt, cfg.SkipPermissions, iss.Number, logf); err != nil {
			return fmt.Errorf("fix-conflict %w", err)
		}

		logf("drain: [%s] waiting for fix-conflict agent %s (timeout=%dms)", iss.Number, fixName, cfg.AgentTimeoutMS)
		fixStatus, err := runner.AgentWait(fixName, cfg.AgentTimeoutMS)
		if err != nil {
			return fmt.Errorf("fix-conflict agent wait: %w", err)
		}
		if fixStatus != "done" {
			// blocked or timeout: leave the base workspace open for the operator.
			return fmt.Errorf("fix-conflict agent %s for issue %s ended with status %q — operator attention required",
				fixName, iss.Number, fixStatus)
		}

		// Verify the merge actually completed — don't trust the agent's "done".
		// If it's still mid-merge, surface it and leave the base workspace open.
		resolved, err := runner.MergeResolved(cfg.RepoPath)
		if err != nil {
			return fmt.Errorf("verify merge resolved: %w", err)
		}
		if !resolved {
			return fmt.Errorf("fix-conflict agent %s reported done but %s is still mid-merge (conflict unresolved) — operator attention required",
				fixName, cfg.RepoPath)
		}
		logf("drain: [%s] fix-conflict agent done — merge completed", iss.Number)

		// Success: close the base workspace we opened for the fix-conflict agent.
		if e := runner.ClosePane(basePaneID); e != nil {
			logf("drain: [%s] closing base workspace pane failed (non-fatal): %v", iss.Number, e)
		}
	} else {
		logf("drain: [%s] clean merge — branch %s landed", iss.Number, iss.Branch)
	}

	// Remove the worktree (both clean-merge and post-conflict paths).
	logf("drain: [%s] removing worktree workspace=%s", iss.Number, workspaceID)
	if err := runner.WorktreeRemove(workspaceID); err != nil {
		return fmt.Errorf("worktree remove: %w", err)
	}

	// The branch is merged now; delete it so merged branches don't accumulate.
	// Non-fatal: a lingering branch is harmless, so don't fail the issue over it.
	if err := runner.DeleteBranch(cfg.RepoPath, iss.Branch); err != nil {
		logf("drain: [%s] merged-branch delete failed (non-fatal): %v", iss.Number, err)
	}

	return nil
}

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
		"commit your work on this branch when you resolve, and do not merge or switch branches — the drain merges it back.",
		iss.Path, iss.Branch)
}
