package drain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestConfigKeyIsPrimaryCheckout: a subdirectory, a relative path and a linked
// worktree all key drain.repos by the primary checkout, like branch_prefix.
func TestConfigKeyIsPrimaryCheckout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=capataz", "GIT_AUTHOR_EMAIL=capataz@example.com",
			"GIT_COMMITTER_NAME=capataz", "GIT_COMMITTER_EMAIL=capataz@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(repo, "init")
	git(repo, "commit", "--allow-empty", "-m", "init")
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-b", "feature-x", wt)

	resolve := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, in := range []string{repo, sub, wt} {
		if got := resolve(configKey(in)); got != repo {
			t.Errorf("configKey(%q) = %q, want %q", in, got, repo)
		}
	}
	t.Chdir(sub)
	if got := resolve(configKey(".")); got != repo {
		t.Errorf("configKey(.) from subdir = %q, want %q", got, repo)
	}

	notRepo := t.TempDir()
	if got := configKey(notRepo); got != notRepo {
		t.Errorf("configKey(non-repo) = %q, want it unchanged", got)
	}
}
