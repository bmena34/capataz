package drain

import (
	"sync"
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
