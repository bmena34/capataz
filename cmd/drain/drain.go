package main

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
	logf := func(format string, args ...any) {
		fmt.Fprintf(cfg.Out, format+"\n", args...)
	}

	if cfg.AgentTimeoutMS <= 0 {
		cfg.AgentTimeoutMS = 3_600_000 // 1 hour default
	}

	logf("drain: feature=%s concurrency=%d skip_permissions=%v dry_run=%v",
		cfg.Feature, cfg.Concurrency, cfg.SkipPermissions, cfg.DryRun)

	for round := 1; ; round++ {
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

		logf("drain: round %d — wave has %d total issue(s), dispatching %d",
			round, len(waves.Waves[0]), len(batch))

		if cfg.DryRun {
			printDryRun(cfg, batch, round)
			// In dry-run, don't loop — just print the first wave and exit.
			return nil
		}

		results := dispatchBatch(cfg, runner, batch, logf)

		// Surface any blocked/timeout/error outcomes for operator attention.
		// The loop continues regardless so remaining issues (future waves) are
		// not abandoned because one issue stalled.
		allDone := true
		for _, res := range results {
			switch res.Status {
			case "done":
				logf("drain: [%s] done", res.Issue.Number)
			case "blocked":
				allDone = false
				logf("drain: [%s] BLOCKED — operator attention required (path: %s)",
					res.Issue.Number, res.Issue.Path)
			case "timeout":
				allDone = false
				logf("drain: [%s] TIMEOUT after %dms — operator attention required (path: %s)",
					res.Issue.Number, cfg.AgentTimeoutMS, res.Issue.Path)
			default:
				allDone = false
				logf("drain: [%s] ERROR: %v", res.Issue.Number, res.Err)
			}
		}

		if !allDone {
			// Some issues were blocked/timed out. Re-scan: capataz issue waves
			// will omit resolved issues and we can pick up the next wave.
			logf("drain: some issues need attention — re-scanning")
		}
	}
}

// dispatchBatch claims, creates worktrees, starts agents, and waits for all
// issues in batch concurrently. It returns one IssueResult per issue.
func dispatchBatch(cfg Config, runner Runner, batch []WaveIssue, logf func(string, ...any)) []IssueResult {
	results := make([]IssueResult, len(batch))
	var wg sync.WaitGroup
	for i, iss := range batch {
		wg.Add(1)
		go func(idx int, issue WaveIssue) {
			defer wg.Done()
			results[idx] = dispatchOne(cfg, runner, issue, logf)
		}(i, iss)
	}
	wg.Wait()
	return results
}

// dispatchOne handles a single issue: claim → worktree → agent start → wait → resolve → merge → cleanup.
func dispatchOne(cfg Config, runner Runner, iss WaveIssue, logf func(string, ...any)) IssueResult {
	fail := func(status string, err error) IssueResult {
		return IssueResult{Issue: iss, Status: status, Err: err}
	}

	// Step a: claim the issue (M1→M2 compare-and-set).
	logf("drain: [%s] claiming %s", iss.Number, iss.Path)
	if err := runner.Claim(iss.Path); err != nil {
		return fail("error", fmt.Errorf("claim: %w", err))
	}

	// Step b: create a herdr worktree for this branch.
	logf("drain: [%s] creating worktree branch=%s", iss.Number, iss.Branch)
	workspaceID, err := runner.WorktreeCreate(iss.Branch, cfg.RepoPath)
	if err != nil {
		return fail("error", fmt.Errorf("worktree create: %w", err))
	}

	// Resolve the pane ID in the new workspace.
	paneID, err := runner.PaneList(workspaceID)
	if err != nil {
		return fail("error", fmt.Errorf("pane list workspace=%s: %w", workspaceID, err))
	}

	// Step c: start a Claude agent in the worktree pane.
	// The minimal prompt tells capataz-work the absolute issue path. It
	// self-detects isolated-worktree mode from the pane's cwd.
	agentName := fmt.Sprintf("drain-%s-%s", cfg.Feature, iss.Number)
	prompt := fmt.Sprintf("run /capataz-work on %s in this worktree", iss.Path)
	logf("drain: [%s] starting agent %s in pane %s", iss.Number, agentName, paneID)
	if err := runner.AgentStart(agentName, paneID, prompt, cfg.SkipPermissions); err != nil {
		return fail("error", fmt.Errorf("agent start: %w", err))
	}

	// Step d: wait for the agent on the unfocused pane. We pass the agent name
	// as the target (herdr resolves name → pane → agent). We treat "blocked"
	// and "timeout" as needs-operator-attention: surface them and don't resolve.
	logf("drain: [%s] waiting for agent %s (timeout=%dms)", iss.Number, agentName, cfg.AgentTimeoutMS)
	status, err := runner.AgentWait(agentName, cfg.AgentTimeoutMS)
	if err != nil {
		return fail("error", fmt.Errorf("agent wait: %w", err))
	}
	if status != "done" {
		// blocked or timeout: surface for operator, don't resolve.
		return fail(status, nil)
	}

	// Step e: resolve the issue on success.
	logf("drain: [%s] resolving %s", iss.Number, iss.Path)
	if err := runner.Resolve(iss.Path); err != nil {
		return fail("error", fmt.Errorf("resolve: %w", err))
	}

	// Step f: merge the worktree branch back into the base repo, then remove
	// the worktree. Conflict → run a fix-conflict agent first.
	logf("drain: [%s] merging branch %s into base %s", iss.Number, iss.Branch, cfg.RepoPath)
	if res := mergeBack(cfg, runner, iss, workspaceID, logf); res != nil {
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
		baseWsID, err := runner.WorktreeOpen(cfg.RepoPath)
		if err != nil {
			return fmt.Errorf("open base workspace for fix-conflict: %w", err)
		}
		basePaneID, err := runner.PaneList(baseWsID)
		if err != nil {
			return fmt.Errorf("pane list base workspace=%s: %w", baseWsID, err)
		}

		// Start a fix-conflict agent in the base repo (where the merge is in progress).
		fixName := fmt.Sprintf("fix-conflict-%s-%s", cfg.Feature, iss.Number)
		fixPrompt := "run /capataz-fix-conflict — resolve the in-progress conflicted merge in this repo and complete it."
		logf("drain: [%s] starting fix-conflict agent %s in pane %s", iss.Number, fixName, basePaneID)
		if err := runner.AgentStart(fixName, basePaneID, fixPrompt, cfg.SkipPermissions); err != nil {
			return fmt.Errorf("fix-conflict agent start: %w", err)
		}

		logf("drain: [%s] waiting for fix-conflict agent %s (timeout=%dms)", iss.Number, fixName, cfg.AgentTimeoutMS)
		fixStatus, err := runner.AgentWait(fixName, cfg.AgentTimeoutMS)
		if err != nil {
			return fmt.Errorf("fix-conflict agent wait: %w", err)
		}
		if fixStatus != "done" {
			// blocked or timeout: the conflict needs operator attention; surface it.
			return fmt.Errorf("fix-conflict agent %s for issue %s ended with status %q — operator attention required",
				fixName, iss.Number, fixStatus)
		}
		logf("drain: [%s] fix-conflict agent done — conflict resolved", iss.Number)
	} else {
		logf("drain: [%s] clean merge — branch %s landed", iss.Number, iss.Branch)
	}

	// Remove the worktree (both clean-merge and post-conflict paths).
	logf("drain: [%s] removing worktree workspace=%s", iss.Number, workspaceID)
	if err := runner.WorktreeRemove(workspaceID); err != nil {
		return fmt.Errorf("worktree remove: %w", err)
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
		prompt := fmt.Sprintf("run /capataz-work on %s in this worktree", iss.Path)
		fmt.Fprintf(cfg.Out, "  [%d] issue %s — %s\n", i+1, iss.Number, iss.Path)
		fmt.Fprintf(cfg.Out, "      branch : %s\n", iss.Branch)
		fmt.Fprintf(cfg.Out, "      cmd[1] : capataz issue claim %s\n", iss.Path)
		fmt.Fprintf(cfg.Out, "      cmd[2] : herdr worktree create --no-focus --branch %s --cwd %s\n",
			iss.Branch, cfg.RepoPath)
		fmt.Fprintf(cfg.Out, "      cmd[3] : herdr agent start %s --kind claude --pane <pane-id> -- %q\n",
			agentName, prompt)
		fmt.Fprintf(cfg.Out, "      cmd[4] : herdr agent wait %s --until done --until blocked --timeout %d\n",
			agentName, cfg.AgentTimeoutMS)
		fmt.Fprintf(cfg.Out, "      cmd[5] : capataz issue resolve %s  (on success)\n", iss.Path)
		fmt.Fprintf(cfg.Out, "      cmd[6] : git -C %s merge --no-ff %s\n", cfg.RepoPath, iss.Branch)
		fmt.Fprintf(cfg.Out, "      cmd[7] : herdr worktree remove --workspace <workspace-id>  (clean merge)\n")
		fmt.Fprintf(cfg.Out, "             : OR start fix-conflict agent + wait + remove  (on conflict)\n\n")
	}
	fmt.Fprintf(cfg.Out, "(No worktrees created, no agents started, no merges run.)\n")
}
