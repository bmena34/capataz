package drain

import (
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
