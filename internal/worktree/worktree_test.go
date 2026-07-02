package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// evalSymlinks resolves symlinks so comparisons hold on macOS, where TempDir
// hands back /var/... paths that git reports as /private/var/....
func evalSymlinks(t *testing.T, path string) string {
	t.Helper()
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return p
}

// initRepo creates a git repo with one commit in a temp dir and returns its
// symlink-resolved path. Skips the whole test if git isn't on PATH.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		// Force a deterministic identity so `git commit` works in CI sandboxes
		// with no global git config.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=capataz", "GIT_AUTHOR_EMAIL=capataz@example.com",
			"GIT_COMMITTER_NAME=capataz", "GIT_COMMITTER_EMAIL=capataz@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("-c", "user.email=capataz@example.com", "-c", "user.name=capataz", "commit", "--allow-empty", "-m", "init")
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

func TestWorktreeLifecycle(t *testing.T) {
	repo := initRepo(t)
	dest := filepath.Join(t.TempDir(), "wt")

	if err := Add(repo, dest, "feature-x"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	destResolved := evalSymlinks(t, dest)

	// From inside the linked worktree, the primary must still resolve to repo.
	got, err := PrimaryCheckout(dest)
	if err != nil {
		t.Fatalf("PrimaryCheckout(worktree): %v", err)
	}
	if evalSymlinks(t, got) != repo {
		t.Fatalf("PrimaryCheckout(worktree) = %q, want %q", got, repo)
	}

	// List (queried from the worktree) includes both the primary and the linked.
	paths, err := List(dest)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var sawPrimary, sawLinked bool
	for _, p := range paths {
		switch evalSymlinks(t, p) {
		case repo:
			sawPrimary = true
		case destResolved:
			sawLinked = true
		}
	}
	if !sawPrimary || !sawLinked {
		t.Fatalf("List = %v, want to include primary %q and linked %q", paths, repo, destResolved)
	}

	if err := Remove(repo, dest); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present after Remove: stat err=%v", err)
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
