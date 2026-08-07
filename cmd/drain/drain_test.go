package main

import (
	"strings"
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

	calls []fakeCall
}

func (f *fakeRunner) record(op string, args ...string) {
	f.calls = append(f.calls, fakeCall{Op: op, Args: args})
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

func (f *fakeRunner) Claim(path string) error {
	f.record("Claim", path)
	return nil
}

func (f *fakeRunner) Resolve(path string) error {
	f.record("Resolve", path)
	return nil
}

func (f *fakeRunner) WorktreeCreate(branch, cwd string) (string, error) {
	f.record("WorktreeCreate", branch, cwd)
	return "ws-fake-" + branch, nil
}

func (f *fakeRunner) PaneList(workspaceID string) (string, error) {
	f.record("PaneList", workspaceID)
	return "p-fake-" + workspaceID, nil
}

func (f *fakeRunner) AgentStart(name, paneID, prompt string, skipPerms bool) error {
	f.record("AgentStart", name, paneID, prompt)
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
	// Should have called Claim + Resolve for each issue.
	if n := countCalls(runner.calls, "Claim"); n != 2 {
		t.Errorf("Claim calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "Resolve"); n != 2 {
		t.Errorf("Resolve calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "WorktreeCreate"); n != 2 {
		t.Errorf("WorktreeCreate calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "AgentStart"); n != 2 {
		t.Errorf("AgentStart calls: got %d, want 2", n)
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
	if n := countCalls(runner.calls, "Claim"); n != 2 {
		t.Errorf("Claim calls: got %d, want 2 (cap enforced)", n)
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
	if n := countCalls(runner.calls, "Claim"); n != 1 {
		t.Errorf("Claim calls: got %d, want 1", n)
	}
	// Resolve must NOT be called when blocked.
	if n := countCalls(runner.calls, "Resolve"); n != 0 {
		t.Errorf("Resolve calls: got %d, want 0 (blocked issue must not be resolved)", n)
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
	if n := countCalls(runner.calls, "Resolve"); n != 0 {
		t.Errorf("Resolve calls: got %d, want 0 (timed-out issue must not be resolved)", n)
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
	if n := countCalls(runner.calls, "Claim"); n != 2 {
		t.Errorf("Claim calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "Resolve"); n != 2 {
		t.Errorf("Resolve calls: got %d, want 2", n)
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
	// Dry-run must NOT call Claim, Resolve, WorktreeCreate, or AgentStart.
	for _, op := range []string{"Claim", "Resolve", "WorktreeCreate", "AgentStart", "AgentWait"} {
		if n := countCalls(runner.calls, op); n != 0 {
			t.Errorf("dry-run must not call %s (got %d calls)", op, n)
		}
	}
	// Output must mention the issues.
	output := out.String()
	if !strings.Contains(output, "DRY RUN") {
		t.Errorf("dry-run output missing 'DRY RUN': %s", output)
	}
	if !strings.Contains(output, "capataz issue claim") {
		t.Errorf("dry-run output missing 'capataz issue claim': %s", output)
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
