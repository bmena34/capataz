package drain

import (
	"strconv"
	"sync"
	"time"
)

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

	// commitsAheadErr, when set, is returned by every CommitsAhead call.
	commitsAheadErr error

	// agentWaitErr maps agent name -> error returned by AgentWait.
	agentWaitErr map[string]error

	// mergeUnresolved makes MergeResolved return false (fix-conflict agent
	// reported done but the merge is still conflicted).
	mergeUnresolved bool

	// dirtyWorktrees maps worktree path -> WorktreeDirty result (absent = clean).
	dirtyWorktrees map[string]bool

	// issueStatus maps issue path -> IssueStatus result (absent = "claimed").
	issueStatus map[string]string

	// issueStatusSeq maps issue path -> successive IssueStatus results, consumed
	// one per call; when exhausted, issueStatus (or "claimed") applies.
	issueStatusSeq map[string][]string

	// answers maps issue path -> successive IssueAnswer results (last repeats;
	// absent = no answers). The drain reads one before starting the agent.
	answers map[string][]fakeAnswer

	// agentWaitSeq maps agent name -> successive AgentWait results, consumed one
	// per call; when exhausted, agentWaitStatus (or "done") applies.
	agentWaitSeq map[string][]string

	// unblockSeq maps agent name -> successive AgentUnblocked results, consumed
	// one per call; when exhausted, "working".
	unblockSeq map[string][]string

	// clock, when set, advances by waitElapsed on every AgentWait and by
	// blockedElapsed on every AgentUnblocked.
	clock          *fakeClock
	waitElapsed    time.Duration
	blockedElapsed time.Duration

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
	if f.commitsAheadErr != nil {
		return 0, f.commitsAheadErr
	}
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

func (f *fakeRunner) WorktreeDirty(path string) (bool, error) {
	f.record("WorktreeDirty", path)
	return f.dirtyWorktrees[path], nil
}

func (f *fakeRunner) IssueStatus(path string) (string, error) {
	f.record("IssueStatus", path)
	f.mu.Lock()
	if seq := f.issueStatusSeq[path]; len(seq) > 0 {
		f.issueStatusSeq[path] = seq[1:]
		f.mu.Unlock()
		return seq[0], nil
	}
	f.mu.Unlock()
	if s, ok := f.issueStatus[path]; ok {
		return s, nil
	}
	return "claimed", nil
}

func (f *fakeRunner) IssueQuestion(path string) (string, error) {
	f.record("IssueQuestion", path)
	return "which table?", nil
}

// fakeAnswer is one IssueAnswer result: the latest answer and the answer count.
type fakeAnswer struct {
	text string
	n    int
}

func (f *fakeRunner) IssueAnswer(path string) (string, int, error) {
	f.record("IssueAnswer", path)
	f.mu.Lock()
	defer f.mu.Unlock()
	seq := f.answers[path]
	if len(seq) == 0 {
		return "", 0, nil
	}
	if len(seq) > 1 {
		f.answers[path] = seq[1:]
	}
	return seq[0].text, seq[0].n, nil
}

func (f *fakeRunner) Notify(title, body string) error {
	f.record("Notify", title, body)
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
	f.record("AgentWait", target, strconv.Itoa(timeoutMS))
	if f.clock != nil {
		f.clock.advance(f.waitElapsed)
	}
	if err := f.agentWaitErr[target]; err != nil {
		return "", err
	}
	if s, ok := f.pop(f.agentWaitSeq, target); ok {
		return s, nil
	}
	if f.agentWaitStatus != nil {
		if s, ok := f.agentWaitStatus[target]; ok {
			return s, nil
		}
	}
	return "done", nil
}

func (f *fakeRunner) AgentUnblocked(target string, timeoutMS int) (string, error) {
	f.record("AgentUnblocked", target, strconv.Itoa(timeoutMS))
	if f.clock != nil {
		f.clock.advance(f.blockedElapsed)
	}
	if s, ok := f.pop(f.unblockSeq, target); ok {
		return s, nil
	}
	return "working", nil
}

// pop consumes the next value of seq[key], if any.
func (f *fakeRunner) pop(seq map[string][]string, key string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(seq[key]) == 0 {
		return "", false
	}
	v := seq[key][0]
	seq[key] = seq[key][1:]
	return v, true
}

// fakeClock is a manual clock for Config.now / Config.sleep.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// callsOf returns the calls matching op, in order.
func callsOf(calls []fakeCall, op string) []fakeCall {
	var out []fakeCall
	for _, c := range calls {
		if c.Op == op {
			out = append(out, c)
		}
	}
	return out
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
