package drain

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// stubHerdr writes a herdr stand-in that prints stdout and exits with code.
func stubHerdr(t *testing.T, stdout string, code int) *realRunner {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "herdr")
	script := "#!/bin/sh\nprintf '%s' '" + stdout + "'\nexit " + strconv.Itoa(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &realRunner{herdrBin: bin}
}

func TestAgentWait_ErrorCodes(t *testing.T) {
	for _, tc := range []struct {
		name, stdout string
		code         int
		want         string
		wantErr      bool
	}{
		{"timeout", `{"error":{"code":"timeout","message":"timed out waiting for agent status"},"id":"cli:agent:wait"}`, 1, "timeout", false},
		{"agent gone", `{"error":{"code":"agent_not_found","message":"no such agent"},"id":"cli:agent:wait"}`, 1, statusGone, false},
		{"other error", `{"error":{"code":"boom","message":"bad"}}`, 1, "error", true},
		{"non-json failure", `oops`, 2, "error", true},
		{"done", `{"result":{"status":"done"}}`, 0, "done", false},
		{"idle folds to done", `{"result":{"state":"idle"}}`, 0, "done", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stubHerdr(t, tc.stdout, tc.code).AgentWait("a", 1000)
			if got != tc.want || (err != nil) != tc.wantErr {
				t.Errorf("AgentWait = %q, %v; want %q, err=%v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}

// herdr095Wait is a captured herdr 0.9.5 `agent wait` success, with the
// agent_status swapped per case. The state lives in result.agent.agent_status.
const herdr095Wait = `{"id":"cli:agent:wait","result":{"agent":{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"109adbc7-7ab3-48ec-a72d-6f8d152d5720"},"agent_status":"%s","completion_seq":87,"cwd":"/Users/me/.herdr/worktrees/capataz/f-13","focused":false,"foreground_cwd":"/Users/me/.herdr/worktrees/capataz/f-13","interactive_ready":true,"name":"drain-f-13","pane_id":"wJ:p1","revision":4,"state_change_seq":188,"tab_id":"wJ:t1","terminal_id":"term_65e614698f88014","terminal_title":"◐ Agent wait","terminal_title_stripped":"Agent wait","tokens":{"context":"[███░░░░░░░] 26%% · 53k"},"workspace_id":"wJ"},"type":"agent_info"}}`

func TestParseAgentWait_Herdr095(t *testing.T) {
	for status, want := range map[string]string{
		"done":    "done",
		"idle":    "done",
		"blocked": "blocked",
		"working": "working",
	} {
		got, err := parseAgentWait("a", []byte(fmt.Sprintf(herdr095Wait, status)), nil, nil)
		if err != nil || got != want {
			t.Errorf("agent_status %q: parseAgentWait = %q, %v; want %q", status, got, err, want)
		}
	}
}

// AgentUnblocked must surface a resumed worker as working, not done.
func TestAgentUnblocked_Working(t *testing.T) {
	got, err := stubHerdr(t, fmt.Sprintf(herdr095Wait, "working"), 0).AgentUnblocked("a", 1000)
	if err != nil || got != "working" {
		t.Errorf("AgentUnblocked = %q, %v; want working", got, err)
	}
}

// IssueStatus reads the file waves emitted directly, so a drain started with a
// relative --repo or from a subdirectory still sees an `asking` worker.
func TestIssueStatus_ReadsFileNotBacklogRoot(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".scratch", "feat", "issues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "01-x.md")
	if err := os.WriteFile(path, []byte("# X\n\nStatus: Asking\n\n## Comments\nStatus: resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "internal")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub) // the drain's cwd/RepoPath no longer matches the backlog root

	r := &realRunner{}
	st, err := r.IssueStatus(path)
	if err != nil || st != "asking" {
		t.Fatalf("IssueStatus = %q, %v; want asking", st, err)
	}
	if _, err := r.IssueStatus(filepath.Join(dir, "02-missing.md")); err == nil {
		t.Fatal("IssueStatus on a missing file: want error")
	}
}

func TestParseAgentTurn(t *testing.T) {
	out := []byte(`{"result":{"agents":[
		{"agent_status":"idle","completion_seq":176,"pane_id":"w2:p1"},
		{"agent_status":"done","completion_seq":4,"name":"drain-f-01","pane_id":"wK:p1"}
	],"type":"agent_list"}}`)
	st, n, err := parseAgentTurn("drain-f-01", out)
	if err != nil || st != "done" || n != 4 {
		t.Errorf("by name: got %q, %d, %v", st, n, err)
	}
	if st, n, _ := parseAgentTurn("w2:p1", out); st != "idle" || n != 176 {
		t.Errorf("by pane: got %q, %d", st, n)
	}
	if st, _, _ := parseAgentTurn("drain-f-02", out); st != statusGone {
		t.Errorf("missing agent: got %q, want %q", st, statusGone)
	}
}
