package drain

import (
	"strings"
	"sync"
	"testing"
)

// TestMergeBack_FixConflictIncomplete covers issue 12: the fix-conflict agent
// reports "done" but the merge is still conflicted (MergeResolved=false). The
// drain must NOT declare success or remove the worktree — it surfaces an error.
func TestMergeBack_FixConflictIncomplete(t *testing.T) {
	const branch = "capataz/f-01-a"
	runner := &fakeRunner{
		agentWaitStatus: map[string]string{"drain-f-01": "done", "fix-conflict-f-01": "done"},
		mergeConflicted: map[string]bool{branch: true},
		mergeUnresolved: true, // agent said done but the conflict remains
	}
	cfg := Config{Feature: "f", AgentTimeoutMS: 5000, RepoPath: "/repo", Out: &strings.Builder{}}
	iss := WaveIssue{Number: "01", Path: "/repo/.scratch/f/issues/01-a.md", Feature: "f", Status: "ready-for-agent", Branch: branch}

	result := dispatchOne(cfg, runner, iss, func(string, ...any) {}, &sync.Mutex{})
	if result.Status != "error" {
		t.Errorf("expected status=error when the merge stays conflicted, got %q", result.Status)
	}
	// Must NOT remove the worktree — the operator needs the conflicted state.
	if n := countCalls(runner.calls, "WorktreeRemove"); n != 0 {
		t.Errorf("WorktreeRemove must not be called when the merge is unresolved; got %d", n)
	}
}

// Merge-back path tests
// ---------------------------------------------------------------------------

// TestMergeBack_CleanMerge verifies the happy path: agent done → Merge
// (no conflict) → WorktreeRemove. No fix-conflict agent is started.
func TestMergeBack_CleanMerge(t *testing.T) {
	const branch = "capataz/f-01-a"
	runner := &fakeRunner{
		wavesResponses: [][]byte{
			[]byte(`{"feature":"f","waves":[[{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"` + branch + `"}]]}`),
			emptyWave("f"),
		},
		// No mergeConflicted entry → Merge returns (false, nil) = clean.
	}
	cfg := Config{
		Feature:        "f",
		Concurrency:    0,
		AgentTimeoutMS: 5000,
		RepoPath:       "/repo",
		Out:            &strings.Builder{},
	}
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Merge must be called once with the issue branch and repo.
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
	// WorktreeRemove must be called once (clean path, no conflict agent).
	if n := countCalls(runner.calls, "WorktreeRemove"); n != 1 {
		t.Errorf("WorktreeRemove calls: got %d, want 1", n)
	}
	// The merged branch is deleted after a successful merge.
	if n := countCalls(runner.calls, "DeleteBranch"); n != 1 {
		t.Errorf("DeleteBranch calls: got %d, want 1 (merged branch cleaned up)", n)
	}
	// OpenBaseWorkspace must NOT be called on a clean merge.
	if n := countCalls(runner.calls, "OpenBaseWorkspace"); n != 0 {
		t.Errorf("OpenBaseWorkspace calls: got %d, want 0 (no conflict)", n)
	}
	// Fix-conflict agent must NOT be started.
	agentStarts := 0
	for _, c := range runner.calls {
		if c.Op == "AgentStart" && len(c.Args) > 0 && strings.HasPrefix(c.Args[0], "fix-conflict-") {
			agentStarts++
		}
	}
	if agentStarts != 0 {
		t.Errorf("fix-conflict AgentStart calls: got %d, want 0", agentStarts)
	}

	// Verify call ordering: Merge comes after the commits check, WorktreeRemove
	// comes after Merge.
	var mergeIdx, verifyIdx, removeIdx int = -1, -1, -1
	for i, c := range runner.calls {
		switch c.Op {
		case "CommitsAhead":
			verifyIdx = i
		case "Merge":
			mergeIdx = i
		case "WorktreeRemove":
			removeIdx = i
		}
	}
	if verifyIdx < 0 || mergeIdx < 0 || removeIdx < 0 {
		t.Fatalf("missing calls: CommitsAhead=%d Merge=%d WorktreeRemove=%d", verifyIdx, mergeIdx, removeIdx)
	}
	if mergeIdx < verifyIdx {
		t.Errorf("Merge (%d) must come after CommitsAhead verify (%d)", mergeIdx, verifyIdx)
	}
	if removeIdx < mergeIdx {
		t.Errorf("WorktreeRemove (%d) must come after Merge (%d)", removeIdx, mergeIdx)
	}
}

// TestMergeBack_ConflictedMerge verifies the conflict path: agent done →
// Merge (conflict) → OpenBaseWorkspace (base repo) → PaneList → fix-conflict
// AgentStart → AgentWait → WorktreeRemove.
func TestMergeBack_ConflictedMerge(t *testing.T) {
	const branch = "capataz/f-01-a"
	runner := &fakeRunner{
		wavesResponses: [][]byte{
			[]byte(`{"feature":"f","waves":[[{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"` + branch + `"}]]}`),
			emptyWave("f"),
		},
		mergeConflicted: map[string]bool{
			branch: true, // Merge returns conflicted=true for this branch.
		},
		// fix-conflict agent resolves successfully (default "done").
	}
	cfg := Config{
		Feature:        "f",
		Concurrency:    0,
		AgentTimeoutMS: 5000,
		RepoPath:       "/repo",
		Out:            &strings.Builder{},
	}
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Merge called once.
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
	// OpenBaseWorkspace called once (to get a pane in the base repo).
	if n := countCalls(runner.calls, "OpenBaseWorkspace"); n != 1 {
		t.Errorf("OpenBaseWorkspace calls: got %d, want 1", n)
	}
	// Two PaneList calls: one for the issue worktree, one for the base workspace.
	if n := countCalls(runner.calls, "PaneList"); n != 2 {
		t.Errorf("PaneList calls: got %d, want 2", n)
	}
	// Two AgentStart calls: one capataz-work, one fix-conflict.
	if n := countCalls(runner.calls, "AgentStart"); n != 2 {
		t.Errorf("AgentStart calls: got %d, want 2", n)
	}
	// Two AgentWait calls: one for capataz-work, one for fix-conflict.
	if n := countCalls(runner.calls, "AgentWait"); n != 2 {
		t.Errorf("AgentWait calls: got %d, want 2", n)
	}
	// WorktreeRemove called once (after fix-conflict done).
	if n := countCalls(runner.calls, "WorktreeRemove"); n != 1 {
		t.Errorf("WorktreeRemove calls: got %d, want 1", n)
	}

	// Verify the fix-conflict agent name pattern and that it ran in the base repo pane.
	var fixStartFound bool
	for _, c := range runner.calls {
		if c.Op == "AgentStart" && strings.HasPrefix(c.Args[0], "fix-conflict-") {
			fixStartFound = true
		}
	}
	if !fixStartFound {
		t.Error("no fix-conflict AgentStart call found")
	}

	// Verify WorktreeRemove comes last (after fix-conflict wait).
	var removeIdx, fixWaitIdx int = -1, -1
	agentWaitCount := 0
	for i, c := range runner.calls {
		if c.Op == "AgentWait" {
			agentWaitCount++
			if agentWaitCount == 2 {
				fixWaitIdx = i // second AgentWait is the fix-conflict one
			}
		}
		if c.Op == "WorktreeRemove" {
			removeIdx = i
		}
	}
	if fixWaitIdx < 0 || removeIdx < 0 {
		t.Fatalf("missing calls: fixWait=%d WorktreeRemove=%d", fixWaitIdx, removeIdx)
	}
	if removeIdx < fixWaitIdx {
		t.Errorf("WorktreeRemove (%d) must come after fix-conflict AgentWait (%d)", removeIdx, fixWaitIdx)
	}
}

// TestMergeBack_ConflictFixBlocked verifies that when the fix-conflict agent
// ends blocked, dispatchOne surfaces an error and does NOT call WorktreeRemove.
// (The issue is already resolved; the conflicted merge is stuck in the base repo.)
func TestMergeBack_ConflictFixBlocked(t *testing.T) {
	const branch = "capataz/f-01-a"
	fixName := "fix-conflict-f-01"
	runner := &fakeRunner{
		mergeConflicted: map[string]bool{branch: true},
		agentWaitStatus: map[string]string{
			fixName: "blocked", // fix-conflict agent blocks
		},
	}
	cfg := Config{
		Feature:        "f",
		Concurrency:    0,
		AgentTimeoutMS: 5000,
		RepoPath:       "/repo",
		Out:            &strings.Builder{},
	}
	iss := WaveIssue{
		Number:  "01",
		Path:    "/repo/.scratch/f/issues/01-a.md",
		Feature: "f",
		Status:  "ready-for-agent",
		Branch:  branch,
	}
	// Set up the fake to return "done" for the capataz-work agent.
	runner.agentWaitStatus["drain-f-01"] = "done"

	logf := func(format string, args ...any) {
		t.Logf(format, args...)
	}
	result := dispatchOne(cfg, runner, iss, logf, &sync.Mutex{})

	// Result must be "error" because fix-conflict blocked.
	if result.Status != "error" {
		t.Errorf("expected status=error, got %q", result.Status)
	}
	if result.Err == nil {
		t.Error("expected non-nil error, got nil")
	}
	// WorktreeRemove must NOT be called when fix-conflict is blocked.
	if n := countCalls(runner.calls, "WorktreeRemove"); n != 0 {
		t.Errorf("WorktreeRemove must not be called when fix-conflict is blocked; got %d calls", n)
	}
}
