package drain

import (
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Wave-parsing tests
// ---------------------------------------------------------------------------

func TestParseWaves_ValidJSON(t *testing.T) {
	raw := []byte(`{
		"feature": "herdr-port",
		"waves": [
			[
				{"number":"01","path":"/repo/.scratch/herdr-port/issues/01-foo.md","feature":"herdr-port","status":"ready-for-agent","branch":"capataz/herdr-port-01-foo"},
				{"number":"02","path":"/repo/.scratch/herdr-port/issues/02-bar.md","feature":"herdr-port","status":"ready-for-agent","branch":"capataz/herdr-port-02-bar"}
			],
			[
				{"number":"03","path":"/repo/.scratch/herdr-port/issues/03-baz.md","feature":"herdr-port","status":"ready-for-agent","branch":"capataz/herdr-port-03-baz"}
			]
		]
	}`)

	out, err := parseWaves(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Feature != "herdr-port" {
		t.Errorf("feature: got %q, want %q", out.Feature, "herdr-port")
	}
	if len(out.Waves) != 2 {
		t.Fatalf("waves: got %d, want 2", len(out.Waves))
	}
	if len(out.Waves[0]) != 2 {
		t.Errorf("wave[0] length: got %d, want 2", len(out.Waves[0]))
	}
	if len(out.Waves[1]) != 1 {
		t.Errorf("wave[1] length: got %d, want 1", len(out.Waves[1]))
	}
	if out.Waves[0][0].Branch != "capataz/herdr-port-01-foo" {
		t.Errorf("branch: got %q", out.Waves[0][0].Branch)
	}
}

func TestParseWaves_EmptyWaves(t *testing.T) {
	raw := []byte(`{"feature":"myfeature","waves":[]}`)
	out, err := parseWaves(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Waves) != 0 {
		t.Errorf("expected empty waves, got %d", len(out.Waves))
	}
}

func TestParseWaves_MissingFeature(t *testing.T) {
	raw := []byte(`{"waves":[]}`)
	_, err := parseWaves(raw)
	if err == nil {
		t.Fatal("expected error for missing feature, got nil")
	}
	if !strings.Contains(err.Error(), "missing feature") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestParseWaves_InvalidJSON(t *testing.T) {
	_, err := parseWaves([]byte(`not-json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

// ---------------------------------------------------------------------------
// dispatchable (concurrency cap) tests
// ---------------------------------------------------------------------------

func makeWave(count int) []WaveIssue {
	out := make([]WaveIssue, count)
	for i := range out {
		out[i] = WaveIssue{
			Number:  strings.Repeat("0", 2-len(string(rune('0'+i)))) + string(rune('0'+i+1)),
			Path:    "/repo/.scratch/f/issues/01-foo.md",
			Feature: "f",
			Status:  "ready-for-agent",
			Branch:  "capataz/f-01-foo",
		}
	}
	return out
}

func TestDispatchable_NoCap(t *testing.T) {
	waves := [][]WaveIssue{makeWave(5)}
	got := dispatchable(waves, 0)
	if len(got) != 5 {
		t.Errorf("cap=0: got %d, want 5", len(got))
	}
}

func TestDispatchable_Cap(t *testing.T) {
	waves := [][]WaveIssue{makeWave(5)}
	got := dispatchable(waves, 3)
	if len(got) != 3 {
		t.Errorf("cap=3: got %d, want 3", len(got))
	}
}

func TestDispatchable_CapLargerThanWave(t *testing.T) {
	waves := [][]WaveIssue{makeWave(2)}
	got := dispatchable(waves, 10)
	if len(got) != 2 {
		t.Errorf("cap>wave: got %d, want 2", len(got))
	}
}

func TestDispatchable_EmptyWaves(t *testing.T) {
	got := dispatchable(nil, 3)
	if got != nil {
		t.Errorf("empty waves: expected nil, got %v", got)
	}
}

func TestDispatchable_SkipsEmptyPath(t *testing.T) {
	waves := [][]WaveIssue{{
		{Number: "01", Path: "", Branch: "b1"}, // bad — no path
		{Number: "02", Path: "/p", Branch: "b2"},
		{Number: "03", Path: "/p2", Branch: ""}, // bad — no branch
		{Number: "04", Path: "/p3", Branch: "b4"},
	}}
	got := dispatchable(waves, 0)
	if len(got) != 2 {
		t.Errorf("skip bad: got %d, want 2", len(got))
	}
}

// ---------------------------------------------------------------------------
// fakeRunner — stub for loop tests
// ---------------------------------------------------------------------------

type fakeCall struct {
	Op   string
	Args []string
}

// fakeRunner records calls and drives configurable outcomes.
type fakeRunner struct {
	// wavesResponses is a queue of JSON responses for successive Waves calls.
	// When exhausted, returns the last entry forever.
	wavesResponses [][]byte
	wavesIdx       int

	// agentWaitStatus maps agent name → status string returned by AgentWait.
	agentWaitStatus map[string]string

	// mergeConflicted maps branch name → whether Merge should return conflicted=true.
	mergeConflicted map[string]bool

	// commitsAhead maps branch name → commits ahead of base returned by CommitsAhead.
	// Absent/nil → default 1 (the agent produced work).
	commitsAhead map[string]int

	// mergeUnresolved makes MergeResolved return false (fix-conflict agent
	// reported done but the merge is still conflicted).
	mergeUnresolved bool

	mu    sync.Mutex
	calls []fakeCall
}

func (f *fakeRunner) record(op string, args ...string) {
	// dispatchBatch runs dispatchOne concurrently, so record is called from
	// multiple goroutines — guard the shared slice.
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{Op: op, Args: args})
	f.mu.Unlock()
}

func (f *fakeRunner) Waves(feature, repo string) ([]byte, error) {
	f.record("Waves", feature, repo)
	if len(f.wavesResponses) == 0 {
		return []byte(`{"feature":"` + feature + `","waves":[]}`), nil
	}
	idx := f.wavesIdx
	if idx >= len(f.wavesResponses) {
		idx = len(f.wavesResponses) - 1
	} else {
		f.wavesIdx++
	}
	return f.wavesResponses[idx], nil
}

func (f *fakeRunner) CommitsAhead(repo, branch string) (int, error) {
	f.record("CommitsAhead", repo, branch)
	if f.commitsAhead != nil {
		if n, ok := f.commitsAhead[branch]; ok {
			return n, nil
		}
	}
	return 1, nil // default: the agent produced work
}

func (f *fakeRunner) WorktreeCreate(branch, cwd string) (string, string, error) {
	f.record("WorktreeCreate", branch, cwd)
	return "ws-fake-" + branch, "/fake/worktrees/" + branch, nil
}

func (f *fakeRunner) TrustDir(path string) error {
	f.record("TrustDir", path)
	return nil
}

func (f *fakeRunner) DeleteBranch(repo, branch string) error {
	f.record("DeleteBranch", repo, branch)
	return nil
}

func (f *fakeRunner) OpenBaseWorkspace(cwd string) (string, error) {
	f.record("OpenBaseWorkspace", cwd)
	return "ws-base-" + cwd, nil
}

func (f *fakeRunner) PaneList(workspaceID string) (string, error) {
	f.record("PaneList", workspaceID)
	return "p-fake-" + workspaceID, nil
}

func (f *fakeRunner) Merge(branch, repo string) (bool, error) {
	f.record("Merge", branch, repo)
	if f.mergeConflicted != nil {
		if conflicted, ok := f.mergeConflicted[branch]; ok {
			return conflicted, nil
		}
	}
	return false, nil
}

func (f *fakeRunner) WorktreeRemove(workspaceID string) error {
	f.record("WorktreeRemove", workspaceID)
	return nil
}

func (f *fakeRunner) AgentStart(name, paneID string, skipPerms bool) error {
	f.record("AgentStart", name, paneID)
	return nil
}

func (f *fakeRunner) AgentPrompt(name, prompt string) error {
	f.record("AgentPrompt", name, prompt)
	return nil
}

func (f *fakeRunner) AgentReachedWorking(name string) bool {
	f.record("AgentReachedWorking", name)
	return true // default: the agent received its prompt
}

func (f *fakeRunner) MergeResolved(repo string) (bool, error) {
	f.record("MergeResolved", repo)
	return !f.mergeUnresolved, nil
}

func (f *fakeRunner) ClosePane(paneID string) error {
	f.record("ClosePane", paneID)
	return nil
}

func (f *fakeRunner) AgentWait(target string, timeoutMS int) (string, error) {
	f.record("AgentWait", target)
	if f.agentWaitStatus != nil {
		if s, ok := f.agentWaitStatus[target]; ok {
			return s, nil
		}
	}
	return "done", nil
}

// countCalls counts how many calls match the given operation.
func countCalls(calls []fakeCall, op string) int {
	n := 0
	for _, c := range calls {
		if c.Op == op {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Drain loop tests
// ---------------------------------------------------------------------------

// twoIssueWave builds a waves JSON response with a single wave of 2 issues.
func twoIssueWave(feature string) []byte {
	return []byte(`{
		"feature": "` + feature + `",
		"waves": [[
			{"number":"01","path":"/repo/.scratch/` + feature + `/issues/01-a.md","feature":"` + feature + `","status":"ready-for-agent","branch":"capataz/` + feature + `-01-a"},
			{"number":"02","path":"/repo/.scratch/` + feature + `/issues/02-b.md","feature":"` + feature + `","status":"ready-for-agent","branch":"capataz/` + feature + `-02-b"}
		]]
	}`)
}

// emptyWave builds an empty waves response (drain complete).
func emptyWave(feature string) []byte {
	return []byte(`{"feature":"` + feature + `","waves":[]}`)
}

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

// ---------------------------------------------------------------------------
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
