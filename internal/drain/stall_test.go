package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stallCfg runs the drain on a fake clock with a fake worktree walk whose
// newest mtime is the latest of writes (offsets from the clock's zero) that
// the clock has passed.
func stallCfg(clock *fakeClock, out *strings.Builder, writes ...time.Duration) Config {
	cfg := askCfg(clock, out)
	cfg.AgentTimeoutMS = int((10 * time.Hour).Milliseconds())
	cfg.StallMinutes = 15
	cfg.walk = func(string) (time.Time, error) {
		var newest time.Time
		for _, w := range writes {
			if t := (time.Time{}).Add(w); !t.After(clock.now()) && t.After(newest) {
				newest = t
			}
		}
		return newest, nil
	}
	return cfg
}

// ticks returns n poll-tick AgentWait results followed by "done".
func ticks(n int) []string {
	seq := make([]string, n, n+1)
	for i := range seq {
		seq[i] = "timeout"
	}
	return append(seq, "done")
}

func stallNotes(calls []fakeCall) []fakeCall {
	var out []fakeCall
	for _, c := range callsOf(calls, "Notify") {
		if strings.Contains(c.Args[0], "hasn't changed anything") {
			out = append(out, c)
		}
	}
	return out
}

func TestStall_NotifiesOncePerStall(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		agentWaitSeq:   map[string][]string{"drain-f-01": ticks(50)},
		clock:          clock,
		waitElapsed:    time.Minute,
	}
	out := &strings.Builder{}
	// Stalls 0→20 min (alert at 15), write at 20 resets, stalls again (alert at 35).
	if err := Drain(stallCfg(clock, out, 0, 20*time.Minute), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	notes := stallNotes(runner.calls)
	if len(notes) != 2 {
		t.Fatalf("stall notifications = %d, want 2 (one per stall)\n%s", len(notes), out)
	}
	if notes[0].Args[0] != "capataz: f 01 hasn't changed anything in 15 min" {
		t.Errorf("title = %q", notes[0].Args[0])
	}
	if b := notes[0].Args[1]; !strings.Contains(b, "p-fake-") || !strings.Contains(b, "/fake/worktrees/b1") {
		t.Errorf("body %q should name the pane and worktree", b)
	}
	if !strings.Contains(out.String(), "STALLED") {
		t.Errorf("stall not logged:\n%s", out)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls = %d, want 1 (a stall alert never stops the agent)", n)
	}
}

func TestStall_SteadyWritesNeverNotify(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		agentWaitSeq:   map[string][]string{"drain-f-01": ticks(40)},
		clock:          clock,
		waitElapsed:    time.Minute,
	}
	out := &strings.Builder{}
	if err := Drain(stallCfg(clock, out, 10*time.Minute, 20*time.Minute, 30*time.Minute), runner); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if n := len(stallNotes(runner.calls)); n != 0 {
		t.Errorf("stall notifications = %d, want 0\n%s", n, out)
	}
}

func TestStall_BlockedTimeDoesNotCount(t *testing.T) {
	clock := &fakeClock{}
	seq := append(ticks(10)[:10], "blocked")
	seq = append(seq, ticks(10)...)
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		agentWaitSeq:   map[string][]string{"drain-f-01": seq},
		clock:          clock,
		waitElapsed:    time.Minute,
		blockedElapsed: 30 * time.Minute,
	}
	out := &strings.Builder{}
	if err := Drain(stallCfg(clock, out, 0), runner); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if n := len(stallNotes(runner.calls)); n != 0 {
		t.Errorf("stall notifications = %d, want 0 (10 min working, 30 blocked, 10 working)\n%s", n, out)
	}
}

func TestStall_AskingNeverNotifies(t *testing.T) {
	clock := &fakeClock{}
	asking := make([]string, 40)
	for i := range asking {
		asking[i] = "asking"
	}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		agentWaitSeq:   map[string][]string{"drain-f-01": ticks(40)},
		issueStatusSeq: map[string][]string{askPath: asking},
		issueStatus:    map[string]string{askPath: "resolved"},
		clock:          clock,
		waitElapsed:    time.Minute,
	}
	out := &strings.Builder{}
	if err := Drain(stallCfg(clock, out, 0), runner); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if n := len(stallNotes(runner.calls)); n != 0 {
		t.Errorf("stall notifications = %d, want 0 while the issue is asking\n%s", n, out)
	}
}

func TestStall_OuterTimeoutStillApplies(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses:  [][]byte{oneIssueWave(), emptyWave("f")},
		agentWaitStatus: map[string]string{"drain-f-01": "timeout"},
		clock:           clock,
		waitElapsed:     time.Minute,
	}
	out := &strings.Builder{}
	cfg := stallCfg(clock, out, 0)
	cfg.AgentTimeoutMS = int((30 * time.Minute).Milliseconds())
	if err := Drain(cfg, runner); err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 30 {
		t.Errorf("AgentWait calls = %d, want 30 (one per minute of a 30 min budget)", n)
	}
	notes := callsOf(runner.calls, "Notify")
	if len(notes) != 2 || !strings.Contains(notes[1].Args[0], "timed out") {
		t.Errorf("Notify = %v, want a stall alert then the timeout", notes)
	}
}

func TestNewestMtime_SkipsPrunedDirs(t *testing.T) {
	root := t.TempDir()
	old := time.Now().Add(-time.Hour)
	for _, p := range []string{"a.go", ".git/HEAD", "node_modules/x/y.js", "vendor/v.go", ".terraform/t"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{root, filepath.Join(root, "a.go"), filepath.Join(root, ".git"), filepath.Join(root, "node_modules"), filepath.Join(root, "vendor"), filepath.Join(root, ".terraform")} {
		if err := os.Chtimes(p, old, old); err != nil {
			t.Fatal(err)
		}
	}
	got, err := newestMtime(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sub(old).Abs() > time.Second {
		t.Errorf("newest = %v, want %v (writes under pruned dirs must be ignored)", got, old)
	}
	if err := os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got2, _ := newestMtime(root); !got2.After(got) {
		t.Errorf("a write to a.go should advance the newest mtime")
	}
}

func TestStall_VanishedAgentEndsPromptly(t *testing.T) {
	// The agent works for two stall polls, then its pane is closed: the wait
	// ends at once instead of polling a missing agent until the budget runs out.
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		agentWaitSeq:   map[string][]string{"drain-f-01": {"timeout", "timeout", statusGone}},
		clock:          clock,
		waitElapsed:    time.Minute,
	}
	out := &strings.Builder{}
	if err := Drain(stallCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 3 {
		t.Errorf("AgentWait calls = %d, want 3 (stop once the agent is gone)", n)
	}
	if n := len(stallNotes(runner.calls)); n != 0 {
		t.Errorf("stall notifications = %d, want 0", n)
	}
	for _, op := range []string{"Merge", "WorktreeRemove"} {
		if n := countCalls(runner.calls, op); n != 0 {
			t.Errorf("%s calls = %d, want 0 (failSafe keeps the claimed worktree)", op, n)
		}
	}
	if !strings.Contains(out.String(), "is gone") {
		t.Errorf("vanished agent not surfaced:\n%s", out)
	}
}
