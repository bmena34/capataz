package drain

import (
	"strings"
	"testing"
)

const resumeCheckout = "/fake/worktrees/b1"

// A worker that ends its turn with a background shell still running is woken
// by Claude Code when the shell finishes; the drain waits for that instead of
// giving up on the dirty worktree.
func TestDrain_ResumedAgentMerges(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		commitsAhead:   map[string]int{"b1": 0},
		dirtyWorktrees: map[string]bool{resumeCheckout: true},
		resumeSeq:      map[string][]string{"drain-f-01": {"working"}},
		clock:          clock,
	}
	runner.onResume = func() {
		runner.commitsAhead["b1"] = 1
		runner.dirtyWorktrees[resumeCheckout] = false
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if strings.Contains(out.String(), "BLOCKED") {
		t.Errorf("resumed agent reported BLOCKED:\n%s", out)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1\n%s", n, out)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 2 {
		t.Errorf("AgentWait calls: got %d, want 2 (turn end, resumed turn)", n)
	}
	if n := countCalls(runner.calls, "AgentResumed"); n != 1 {
		t.Errorf("AgentResumed calls: got %d, want 1", n)
	}
}

func TestDrain_IdleAgentPastGraceKeepsWorktree(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		commitsAhead:   map[string]int{"b1": 0},
		dirtyWorktrees: map[string]bool{resumeCheckout: true},
		clock:          clock,
	}
	out := &strings.Builder{}
	cfg := askCfg(clock, out)
	cfg.ResumeGraceMS = 2_000
	_ = Drain(cfg, runner)

	calls := callsOf(runner.calls, "AgentResumed")
	if len(calls) != 1 || calls[0].Args[1] != "2000" {
		t.Errorf("AgentResumed calls = %v, want one with the 2000ms window", calls)
	}
	if !strings.Contains(out.String(), "BLOCKED") {
		t.Errorf("idle agent not reported BLOCKED:\n%s", out)
	}
	if n := countCalls(runner.calls, "Merge"); n != 0 {
		t.Errorf("Merge calls: got %d, want 0", n)
	}
	if n := countCalls(runner.calls, "WorktreeRemove"); n != 0 {
		t.Errorf("dirty worktree removed (%d WorktreeRemove calls)", n)
	}
}

func TestDrain_ResumeGraceCappedByBudget(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		commitsAhead:   map[string]int{"b1": 0},
		dirtyWorktrees: map[string]bool{resumeCheckout: true},
		clock:          clock,
		waitElapsed:    9_000_000_000, // 9s of the 10s budget
	}
	out := &strings.Builder{}
	_ = Drain(askCfg(clock, out), runner)
	calls := callsOf(runner.calls, "AgentResumed")
	if len(calls) != 1 || calls[0].Args[1] != "1000" {
		t.Errorf("AgentResumed calls = %v, want one capped at the 1000ms left", calls)
	}
}

func TestDrain_AskingSkipsResumeGrace(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "asking", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if len(answerPrompts(runner.calls)) != 1 {
		t.Errorf("answer not delivered:\n%s", out)
	}
	// The question path ran first; the grace check only ran once the work was
	// done (one commit, clean), so it never waited.
	if n := countCalls(runner.calls, "AgentResumed"); n != 0 {
		t.Errorf("AgentResumed calls: got %d, want 0", n)
	}
}

func TestWorkPromptForbidsBackgroundShellAtTurnEnd(t *testing.T) {
	p := workPrompt(WaveIssue{Path: askPath, Branch: "b1"})
	if !strings.Contains(p, "foreground") || !strings.Contains(p, "background shell") {
		t.Errorf("workPrompt lacks the background-shell rule: %q", p)
	}
}
