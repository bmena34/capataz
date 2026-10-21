package drain

import (
	"strings"
	"testing"
	"time"
)

const askPath = "/repo/.scratch/f/issues/01-a.md"

func oneIssueWave() []byte {
	return []byte(`{"feature":"f","waves":[[
		{"number":"01","path":"` + askPath + `","feature":"f","status":"ready-for-agent","branch":"b1"}
	]]}`)
}

// askCfg runs the drain on a fake clock: sleep advances it instead of blocking.
func askCfg(clock *fakeClock, out *strings.Builder) Config {
	return Config{
		Feature:        "f",
		AgentTimeoutMS: 10_000,
		RepoPath:       "/repo",
		Out:            out,
		askPoll:        time.Hour,
		now:            clock.now,
		sleep:          clock.advance,
	}
}

// answerPrompts returns the AgentPrompt texts that delivered an answer.
func answerPrompts(calls []fakeCall) []string {
	var out []string
	for _, c := range callsOf(calls, "AgentPrompt") {
		if strings.HasPrefix(c.Args[1], "Your question on") {
			out = append(out, c.Args[1])
		}
	}
	return out
}

func TestDrain_AskingWaitsAndDeliversAnswer(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "asking", "claimed"}},
		answers:        map[string][]string{askPath: {"cookies"}},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}

	if n := countCalls(runner.calls, "Notify"); n != 1 {
		t.Errorf("Notify calls: got %d, want 1", n)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 2 {
		t.Errorf("AgentWait calls: got %d, want 2 (wait, answer, wait again)", n)
	}
	got := answerPrompts(runner.calls)
	if len(got) != 1 || !strings.Contains(got[0], "answered: cookies. Continue the issue.") {
		t.Errorf("answer prompts = %q", got)
	}
	for _, c := range callsOf(runner.calls, "AgentPrompt") {
		if c.Args[0] != "drain-f-01" {
			t.Errorf("prompted agent %q, want the same agent drain-f-01", c.Args[0])
		}
	}
	// Nothing verified or torn down while the question was open.
	firstAnswer := -1
	for i, c := range runner.calls {
		if c.Op == "AgentPrompt" && strings.HasPrefix(c.Args[1], "Your question on") {
			firstAnswer = i
			break
		}
	}
	if firstAnswer < 0 {
		t.Fatal("no answer prompt delivered")
	}
	for _, c := range runner.calls[:firstAnswer] {
		switch c.Op {
		case "CommitsAhead", "WorktreeDirty", "WorktreeRemove", "DeleteBranch", "Merge":
			t.Errorf("%s called before the answer was delivered", c.Op)
		}
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
	if !strings.Contains(out.String(), "capataz issue answer "+askPath) {
		t.Errorf("log does not name the answer command:\n%s", out)
	}
	notify := callsOf(runner.calls, "Notify")[0]
	if notify.Args[0] != "capataz: f 01 has a question" || notify.Args[1] != "which table?" {
		t.Errorf("Notify args = %q", notify.Args)
	}
}

func TestDrain_TwoQuestionsBothDelivered(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "claimed", "asking", "claimed"}},
		answers:        map[string][]string{askPath: {"first", "second"}},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if n := countCalls(runner.calls, "Notify"); n != 2 {
		t.Errorf("Notify calls: got %d, want 2", n)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 3 {
		t.Errorf("AgentWait calls: got %d, want 3", n)
	}
	got := answerPrompts(runner.calls)
	if len(got) != 2 || !strings.Contains(got[0], "answered: first.") || !strings.Contains(got[1], "answered: second.") {
		t.Errorf("answer prompts = %q", got)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

func TestDrain_AskingTimeExcludedFromTimeout(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		// Two hour-long polls while asking — far past the 10s agent timeout.
		issueStatusSeq: map[string][]string{askPath: {"asking", "asking", "claimed"}},
		answers:        map[string][]string{askPath: {"yes"}},
		clock:          clock,
		waitElapsed:    3 * time.Second,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	waits := callsOf(runner.calls, "AgentWait")
	if len(waits) != 2 {
		t.Fatalf("AgentWait calls: got %d, want 2", len(waits))
	}
	if waits[0].Args[1] != "10000" || waits[1].Args[1] != "7000" {
		t.Errorf("AgentWait timeouts = %s, %s; want 10000, 7000 (only agent time counts)", waits[0].Args[1], waits[1].Args[1])
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

func TestDrain_AskingToNeedsInfoKeepsWorktree(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "asking", "needs-info"}},
		issueStatus:    map[string]string{askPath: "needs-info"},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 1 {
		t.Errorf("AgentWait calls: got %d, want 1 (stop waiting)", n)
	}
	if got := answerPrompts(runner.calls); len(got) != 0 {
		t.Errorf("answer delivered for a needs-info issue: %q", got)
	}
	for _, op := range []string{"WorktreeRemove", "DeleteBranch", "Merge"} {
		if n := countCalls(runner.calls, op); n != 0 {
			t.Errorf("%s calls: got %d, want 0 (worktree kept)", op, n)
		}
	}
	if !strings.Contains(out.String(), "BLOCKED") {
		t.Errorf("drain did not surface the issue as blocked:\n%s", out)
	}
}

func TestWorkPromptAsksViaCLI(t *testing.T) {
	p := workPrompt(WaveIssue{Path: askPath, Branch: "b1"})
	if !strings.Contains(p, "capataz issue ask "+askPath) || !strings.Contains(p, "never ask in chat") {
		t.Errorf("workPrompt lacks the ask instruction: %s", p)
	}
}
