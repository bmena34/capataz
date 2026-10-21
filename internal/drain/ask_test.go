package drain

import (
	"strings"
	"testing"
	"time"

	"github.com/bmena34/capataz/internal/issues"
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
		if strings.HasPrefix(c.Args[1], "Your question") {
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
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
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
		answers:        map[string][]fakeAnswer{askPath: {{}, {"first", 1}, {"second", 2}}},
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

// The operator answers while the agent is still working: by the time it ends
// its turn the issue is back to claimed. The answer must still be delivered.
func TestDrain_EarlyAnswerDelivered(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	got := answerPrompts(runner.calls)
	if len(got) != 1 || !strings.Contains(got[0], "answered: cookies.") {
		t.Errorf("answer prompts = %q", got)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 2 {
		t.Errorf("AgentWait calls: got %d, want 2 (wait, answer, wait again)", n)
	}
	waits := 0
	for _, c := range runner.calls {
		switch c.Op {
		case "AgentWait":
			waits++
		case "CommitsAhead":
			if waits < 2 {
				t.Errorf("commits verified after the first done, before the answer was delivered")
			}
		}
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

// The worker asks Q1 and keeps working; the operator answers A1 while it is
// still in its turn; it then asks Q2 and ends its turn. The one prompt that
// follows A2 carries both answers, each with its question.
func TestDrain_EarlyAnswerThenSecondQuestionBothDelivered(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"second", 2}}},
		exchanges: map[string][]issues.Exchange{askPath: {
			{Question: "which table?", Answer: "first"},
			{Question: "which column?", Answer: "second"},
		}},
		clock: clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	got := answerPrompts(runner.calls)
	if len(got) != 1 {
		t.Fatalf("answer prompts = %q, want one", got)
	}
	for _, want := range []string{"Q: which table?", "A: first", "Q: which column?", "A: second", "Continue the issue."} {
		if !strings.Contains(got[0], want) {
			t.Errorf("answer prompt %q lacks %q", got[0], want)
		}
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

// Answers already on the issue before the agent started are not re-delivered.
func TestDrain_OldAnswerNotRedelivered(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		answers:        map[string][]fakeAnswer{askPath: {{"old", 1}}},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if got := answerPrompts(runner.calls); len(got) != 0 {
		t.Errorf("answer prompts = %q, want none", got)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

// A torn read mid-answer (status "") keeps the asking wait going rather than
// ending the dispatch and dropping the answer.
func TestDrain_AskingSurvivesTornStatusRead(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
		clock:          clock,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	got := answerPrompts(runner.calls)
	if len(got) != 1 || !strings.Contains(got[0], "answered: cookies") {
		t.Errorf("answer prompts = %q\n%s", got, out)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1\n%s", n, out)
	}
}

func TestDrain_AskingTimeExcludedFromTimeout(t *testing.T) {
	clock := &fakeClock{}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		// Two hour-long polls while asking — far past the 10s agent timeout.
		issueStatusSeq: map[string][]string{askPath: {"asking", "asking", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"yes", 1}}},
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

// herdr's agent wait matches the done state the asking turn ended in, so the
// drain must wait for the agent to pick up the answer before waiting again.
func TestDrain_AnswerWaitsForNewTurn(t *testing.T) {
	clock := &fakeClock{}
	turns := &fakeTurns{lag: 3}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
		clock:          clock,
		turns:          turns,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if turns.staleWaits != 0 {
		t.Errorf("AgentWait matched the pre-answer done %d time(s)", turns.staleWaits)
	}
	if got := answerPrompts(runner.calls); len(got) != 1 {
		t.Errorf("answer prompts = %q, want 1 (picked up late, not dropped)", got)
	}
	if n := countCalls(runner.calls, "AgentWait"); n != 2 {
		t.Errorf("AgentWait calls: got %d, want 2", n)
	}
	if turns.completion != 2 {
		t.Errorf("turns completed: got %d, want 2 (ask, then the answer)", turns.completion)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

// A dropped answer prompt is resubmitted once the resume window passes.
func TestDrain_DroppedAnswerResubmitted(t *testing.T) {
	clock := &fakeClock{}
	turns := &fakeTurns{drop: 1}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
		clock:          clock,
		turns:          turns,
	}
	out := &strings.Builder{}
	if err := Drain(askCfg(clock, out), runner); err != nil {
		t.Fatalf("Drain: %v\n%s", err, out)
	}
	if got := answerPrompts(runner.calls); len(got) != 2 {
		t.Errorf("answer prompts = %d, want 2 (dropped, resubmitted)", len(got))
	}
	if turns.staleWaits != 0 {
		t.Errorf("AgentWait matched the pre-answer done %d time(s)", turns.staleWaits)
	}
	if n := countCalls(runner.calls, "Merge"); n != 1 {
		t.Errorf("Merge calls: got %d, want 1", n)
	}
}

// An agent that never picks up the answer fails the dispatch, keeping its work.
func TestDrain_AnswerNeverPickedUp(t *testing.T) {
	clock := &fakeClock{}
	turns := &fakeTurns{drop: 3}
	runner := &fakeRunner{
		wavesResponses: [][]byte{oneIssueWave(), emptyWave("f")},
		issueStatusSeq: map[string][]string{askPath: {"asking", "claimed"}},
		answers:        map[string][]fakeAnswer{askPath: {{}, {"cookies", 1}}},
		clock:          clock,
		turns:          turns,
	}
	out := &strings.Builder{}
	_ = Drain(askCfg(clock, out), runner)
	if n := countCalls(runner.calls, "AgentWait"); n != 1 {
		t.Errorf("AgentWait calls: got %d, want 1", n)
	}
	if n := countCalls(runner.calls, "Merge"); n != 0 {
		t.Errorf("Merge calls: got %d, want 0", n)
	}
	if n := strings.Count(out.String(), "has not resumed after the answer"); n != 3 {
		t.Errorf("resume retries logged: got %d, want 3:\n%s", n, out)
	}
	if got := answerPrompts(runner.calls); len(got) != 3 {
		t.Errorf("answer prompts = %d, want 3", len(got))
	}
}
