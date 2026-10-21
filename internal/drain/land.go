package drain

import (
	"fmt"
	"time"
)

// mergeBack merges iss.Branch into cfg.RepoPath and removes the worktree.
// On conflict it starts a fix-conflict agent in the base repo, waits for done
// (waiting out a blocked agent), then removes the worktree. Returns non-nil only on hard (non-conflict) errors
// or when the fix-conflict agent itself times out.
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

		budget := time.Duration(cfg.AgentTimeoutMS) * time.Millisecond
		fixStatus, _, err := waitAgent(cfg, runner, fixName, basePaneID, fmt.Sprintf("%s %s fix-conflict", cfg.Feature, iss.Number), iss.Number, budget, logf)
		if err != nil {
			return fmt.Errorf("fix-conflict agent wait: %w", err)
		}
		if fixStatus != "done" {
			// timeout: leave the base workspace open for the operator.
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
