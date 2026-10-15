package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// evalSymlinks resolves path so comparisons survive macOS's /var -> /private/var
// temp-dir symlink.
func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return p
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Force a deterministic identity so `git commit` works in CI sandboxes
	// with no global git config.
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=capataz", "GIT_AUTHOR_EMAIL=capataz@example.com",
		"GIT_COMMITTER_NAME=capataz", "GIT_COMMITTER_EMAIL=capataz@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	git(t, repo, "init")
	git(t, repo, "commit", "--allow-empty", "-m", "init")
	return evalSymlinks(t, repo)
}

func TestPrimaryCheckoutPlainRepo(t *testing.T) {
	repo := initRepo(t)
	got, err := PrimaryCheckout(repo)
	if err != nil {
		t.Fatal(err)
	}
	if evalSymlinks(t, got) != repo {
		t.Fatalf("PrimaryCheckout = %q, want %q", got, repo)
	}
}

func TestPrimaryCheckoutFromLinkedWorktree(t *testing.T) {
	repo := initRepo(t)
	dest := filepath.Join(t.TempDir(), "wt")
	git(t, repo, "worktree", "add", "-b", "feature-x", dest)

	got, err := PrimaryCheckout(dest)
	if err != nil {
		t.Fatal(err)
	}
	if evalSymlinks(t, got) != repo {
		t.Fatalf("PrimaryCheckout(linked) = %q, want %q", got, repo)
	}
}

func TestPrimaryCheckoutNotARepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	if _, err := PrimaryCheckout(t.TempDir()); err == nil {
		t.Fatal("PrimaryCheckout on a non-repo dir: want error, got nil")
	}
}
