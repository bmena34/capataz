package drain

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Drain loop tests
// ---------------------------------------------------------------------------

func TestDrain_SingleWaveDone(t *testing.T) {
	// Two issues → both done → empty re-scan → drain exits.
	runner := &fakeRunner{
		wavesResponses: [][]byte{twoIssueWave("f"), emptyWave("f")},
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
	// The drain does NOT claim/resolve (capataz-work does). Each issue should be
	// dispatched (worktree + agent), verified (commits ahead), and merged.
	if n := countCalls(runner.calls, "WorktreeCreate"); n != 2 {
		t.Errorf("WorktreeCreate calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "AgentStart"); n != 2 {
		t.Errorf("AgentStart calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "CommitsAhead"); n != 2 {
		t.Errorf("CommitsAhead calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "Merge"); n != 2 {
		t.Errorf("Merge calls: got %d, want 2", n)
	}
	// Waves called twice: once for real issues, once for empty re-scan.
	if n := countCalls(runner.calls, "Waves"); n != 2 {
		t.Errorf("Waves calls: got %d, want 2", n)
	}
}

func TestDrain_ConcurrencyCap(t *testing.T) {
	// Three issues, cap=2 → first batch takes 2; empty re-scan.
	wave := []byte(`{
		"feature":"f",
		"waves":[[
			{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"b1"},
			{"number":"02","path":"/repo/.scratch/f/issues/02-b.md","feature":"f","status":"ready-for-agent","branch":"b2"},
			{"number":"03","path":"/repo/.scratch/f/issues/03-c.md","feature":"f","status":"ready-for-agent","branch":"b3"}
		]]
	}`)
	runner := &fakeRunner{
		wavesResponses: [][]byte{wave, emptyWave("f")},
	}
	cfg := Config{
		Feature:        "f",
		Concurrency:    2,
		AgentTimeoutMS: 5000,
		RepoPath:       "/repo",
		Out:            &strings.Builder{},
	}
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only 2 of 3 issues dispatched in this round (cap=2).
	if n := countCalls(runner.calls, "AgentStart"); n != 2 {
		t.Errorf("AgentStart calls: got %d, want 2 (cap enforced)", n)
	}
}

func TestDrain_BlockedAgentNotResolved(t *testing.T) {
	// One issue goes blocked — must NOT be resolved; loop re-scans and then drains.
	wave := []byte(`{
		"feature":"f",
		"waves":[[
			{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"b1"}
		]]
	}`)
	agentName := "drain-f-01"
	runner := &fakeRunner{
		wavesResponses: [][]byte{wave, emptyWave("f")},
		agentWaitStatus: map[string]string{
			agentName: "blocked",
		},
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
	if n := countCalls(runner.calls, "AgentStart"); n != 1 {
		t.Errorf("AgentStart calls: got %d, want 1", n)
	}
	// Merge must NOT be called when the agent is blocked (no verified work).
	if n := countCalls(runner.calls, "Merge"); n != 0 {
		t.Errorf("Merge calls: got %d, want 0 (blocked issue must not merge)", n)
	}
}

func TestDrain_TimeoutAgentNotResolved(t *testing.T) {
	agentName := "drain-f-01"
	wave := []byte(`{
		"feature":"f",
		"waves":[[
			{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"b1"}
		]]
	}`)
	runner := &fakeRunner{
		wavesResponses: [][]byte{wave, emptyWave("f")},
		agentWaitStatus: map[string]string{
			agentName: "timeout",
		},
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
	if n := countCalls(runner.calls, "Merge"); n != 0 {
		t.Errorf("Merge calls: got %d, want 0 (timed-out issue must not merge)", n)
	}
}

func TestDrain_MultipleRounds(t *testing.T) {
	// Round 1: wave with 1 issue (done). Round 2: wave with 1 more issue (done).
	// Round 3: empty (drain complete). Total: 3 Waves calls, 2 Claims, 2 Resolves.
	wave1 := []byte(`{"feature":"f","waves":[[{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"b1"}]]}`)
	wave2 := []byte(`{"feature":"f","waves":[[{"number":"02","path":"/repo/.scratch/f/issues/02-b.md","feature":"f","status":"ready-for-agent","branch":"b2"}]]}`)
	runner := &fakeRunner{
		wavesResponses: [][]byte{wave1, wave2, emptyWave("f")},
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
	if n := countCalls(runner.calls, "Waves"); n != 3 {
		t.Errorf("Waves calls: got %d, want 3", n)
	}
	if n := countCalls(runner.calls, "WorktreeCreate"); n != 2 {
		t.Errorf("WorktreeCreate calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "Merge"); n != 2 {
		t.Errorf("Merge calls: got %d, want 2", n)
	}
}

func TestDrain_DryRun(t *testing.T) {
	runner := &fakeRunner{
		wavesResponses: [][]byte{twoIssueWave("f")},
	}
	var out strings.Builder
	cfg := Config{
		Feature:        "f",
		Concurrency:    0,
		AgentTimeoutMS: 5000,
		RepoPath:       "/repo",
		DryRun:         true,
		Out:            &out,
	}
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Dry-run must NOT execute any side effects.
	for _, op := range []string{"WorktreeCreate", "AgentStart", "AgentWait", "CommitsAhead", "Merge"} {
		if n := countCalls(runner.calls, op); n != 0 {
			t.Errorf("dry-run must not call %s (got %d calls)", op, n)
		}
	}
	// Output must mention the issues.
	output := out.String()
	if !strings.Contains(output, "DRY RUN") {
		t.Errorf("dry-run output missing 'DRY RUN': %s", output)
	}
	if !strings.Contains(output, "capataz-work") {
		t.Errorf("dry-run output missing 'capataz-work': %s", output)
	}
	if !strings.Contains(output, "herdr worktree create") {
		t.Errorf("dry-run output missing 'herdr worktree create': %s", output)
	}
}

func TestDrain_SkipPermissionsFlag(t *testing.T) {
	runner := &fakeRunner{
		wavesResponses: [][]byte{
			[]byte(`{"feature":"f","waves":[[{"number":"01","path":"/p/01-a.md","feature":"f","status":"ready-for-agent","branch":"b1"}]]}`),
			emptyWave("f"),
		},
	}
	cfg := Config{
		Feature:         "f",
		Concurrency:     0,
		AgentTimeoutMS:  5000,
		SkipPermissions: true,
		RepoPath:        "/repo",
		Out:             &strings.Builder{},
	}
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// AgentStart should have been called once.
	if n := countCalls(runner.calls, "AgentStart"); n != 1 {
		t.Errorf("AgentStart calls: got %d, want 1", n)
	}
	// The fake records the prompt arg — we trust the real runner to pass
	// --dangerously-skip-permissions; here we just verify the code path ran.
}

// TestDrain_StopsOnRepeatedFailure covers issue 10: an issue that keeps failing
// while staying dispatchable must be tried once and then the drain must stop —
// not loop forever (the live run spun 2007 rounds).
func TestDrain_StopsOnRepeatedFailure(t *testing.T) {
	wave := []byte(`{"feature":"f","waves":[[{"number":"01","path":"/repo/.scratch/f/issues/01-a.md","feature":"f","status":"ready-for-agent","branch":"b1"}]]}`)
	runner := &fakeRunner{
		wavesResponses:  [][]byte{wave}, // last entry repeats forever
		agentWaitStatus: map[string]string{"drain-f-01": "blocked"},
	}
	cfg := Config{Feature: "f", Concurrency: 0, AgentTimeoutMS: 5000, RepoPath: "/repo", Out: &strings.Builder{}}

	if err := Drain(cfg, runner); err == nil {
		t.Fatal("expected drain to stop with an error on repeated failure, got nil")
	}
	if n := countCalls(runner.calls, "AgentStart"); n != 1 {
		t.Errorf("AgentStart calls: got %d, want 1 (a failed issue must not be retried)", n)
	}
}

// TestDispatchOne_EmptyBranchFailsNoMerge covers the live-found bug: the agent
// reaches "done" but produced no commits (e.g. capataz-work refused an
// already-claimed issue). The drain must treat that as a failure — no merge, no
// worktree removal — rather than a silent clean success.
func TestDispatchOne_EmptyBranchFailsNoMerge(t *testing.T) {
	const branch = "capataz/f-01-a"
	runner := &fakeRunner{
		agentWaitStatus: map[string]string{"drain-f-01": "done"},
		commitsAhead:    map[string]int{branch: 0}, // agent committed nothing
	}
	cfg := Config{Feature: "f", AgentTimeoutMS: 5000, RepoPath: "/repo", Out: &strings.Builder{}}
	iss := WaveIssue{Number: "01", Path: "/repo/.scratch/f/issues/01-a.md", Feature: "f", Status: "ready-for-agent", Branch: branch}

	result := dispatchOne(cfg, runner, iss, func(string, ...any) {}, &sync.Mutex{})
	if result.Status != "error" {
		t.Errorf("expected status=error for empty branch, got %q", result.Status)
	}
	if n := countCalls(runner.calls, "Merge"); n != 0 {
		t.Errorf("Merge must not be called for an empty branch; got %d", n)
	}
	// The failed dispatch must clean up its worktree + branch so a re-scan
	// doesn't collide (issue 10).
	if n := countCalls(runner.calls, "WorktreeRemove"); n != 1 {
		t.Errorf("WorktreeRemove (cleanup) calls: got %d, want 1", n)
	}
	if n := countCalls(runner.calls, "DeleteBranch"); n != 1 {
		t.Errorf("DeleteBranch (cleanup) calls: got %d, want 1", n)
	}
}

// zeroCommitCase runs dispatchOne for an agent that finished with no commits
// and returns the result, the fake, and the log.
func zeroCommitCase(t *testing.T, dirty bool, status string) (IssueResult, *fakeRunner, string) {
	t.Helper()
	const branch = "capataz/f-01-a"
	const path = "/repo/.scratch/f/issues/01-a.md"
	runner := &fakeRunner{
		commitsAhead:   map[string]int{branch: 0},
		dirtyWorktrees: map[string]bool{"/fake/worktrees/" + branch: dirty},
		issueStatus:    map[string]string{path: status},
	}
	cfg := Config{Feature: "f", AgentTimeoutMS: 5000, RepoPath: "/repo", Out: &strings.Builder{}}
	iss := WaveIssue{Number: "01", Path: path, Feature: "f", Status: "ready-for-agent", Branch: branch}
	var log strings.Builder
	res := dispatchOne(cfg, runner, iss, func(f string, a ...any) { fmt.Fprintf(&log, f+"\n", a...) }, &sync.Mutex{})
	return res, runner, log.String()
}

func assertKept(t *testing.T, res IssueResult, r *fakeRunner) {
	t.Helper()
	if res.Status != "blocked" {
		t.Errorf("status = %q, want blocked", res.Status)
	}
	for _, op := range []string{"WorktreeRemove", "DeleteBranch", "Merge"} {
		if n := countCalls(r.calls, op); n != 0 {
			t.Errorf("%s calls = %d, want 0", op, n)
		}
	}
}

func TestDispatchOne_ZeroCommitsDirtyWorktreeKept(t *testing.T) {
	res, r, log := zeroCommitCase(t, true, "claimed")
	assertKept(t, res, r)
	if !strings.Contains(log, "/fake/worktrees/capataz/f-01-a") {
		t.Errorf("log should name the worktree path; got:\n%s", log)
	}
}

func TestDispatchOne_ZeroCommitsNeedsInfoKept(t *testing.T) {
	for _, st := range []string{"needs-info", "ready-for-human"} {
		res, r, log := zeroCommitCase(t, false, st)
		assertKept(t, res, r)
		if !strings.Contains(log, "needs an answer") {
			t.Errorf("%s: log should say it needs an answer; got:\n%s", st, log)
		}
	}
}

func TestDispatchOne_ZeroCommitsCleanClaimedCleansUp(t *testing.T) {
	res, r, _ := zeroCommitCase(t, false, "claimed")
	if res.Status != "error" {
		t.Errorf("status = %q, want error", res.Status)
	}
	if n := countCalls(r.calls, "WorktreeRemove"); n != 1 {
		t.Errorf("WorktreeRemove calls = %d, want 1", n)
	}
	if n := countCalls(r.calls, "DeleteBranch"); n != 1 {
		t.Errorf("DeleteBranch calls = %d, want 1", n)
	}
}

// ---------------------------------------------------------------------------

// TestDryRun_ShowsMergeStep verifies that --dry-run output mentions the merge
// and cleanup commands introduced in this issue.
func TestDryRun_ShowsMergeStep(t *testing.T) {
	runner := &fakeRunner{
		wavesResponses: [][]byte{twoIssueWave("f")},
	}
	var out strings.Builder
	cfg := Config{
		Feature:        "f",
		Concurrency:    0,
		AgentTimeoutMS: 5000,
		RepoPath:       "/repo",
		DryRun:         true,
		Out:            &out,
	}
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := out.String()
	// Must mention the merge command.
	if !strings.Contains(output, "git") || !strings.Contains(output, "merge") {
		t.Errorf("dry-run output missing merge step: %s", output)
	}
	// Must mention worktree removal.
	if !strings.Contains(output, "herdr worktree remove") {
		t.Errorf("dry-run output missing 'herdr worktree remove': %s", output)
	}
	// Must NOT call any real operations.
	for _, op := range []string{"Claim", "Resolve", "WorktreeCreate", "AgentStart", "AgentWait", "Merge", "WorktreeRemove"} {
		if n := countCalls(runner.calls, op); n != 0 {
			t.Errorf("dry-run must not call %s (got %d calls)", op, n)
		}
	}
}
