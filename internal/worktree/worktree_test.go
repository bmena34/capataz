package worktree

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitEnv is the deterministic identity so `git commit` works without a global
// git config (CI sandboxes).
func gitEnv() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME=capataz", "GIT_AUTHOR_EMAIL=capataz@example.com",
		"GIT_COMMITTER_NAME=capataz", "GIT_COMMITTER_EMAIL=capataz@example.com")
}

// git runs a git command in dir, failing the test on error.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeFile writes content to repo/name and stages+commits it on the current
// branch with the given message.
func writeCommit(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", "-A")
	git(t, repo, "-c", "user.email=capataz@example.com", "-c", "user.name=capataz", "commit", "-m", msg)
}

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

func TestListWT(t *testing.T) {
	repo := initRepo(t)
	dest := filepath.Join(t.TempDir(), "wt")
	if err := Add(repo, dest, "capataz/feature-01-slug"); err != nil {
		t.Fatalf("Add: %v", err)
	}
	destResolved := evalSymlinks(t, dest)

	wts, err := ListWT(repo)
	if err != nil {
		t.Fatalf("ListWT: %v", err)
	}
	var linkedBranch string
	var sawPrimary bool
	for _, wt := range wts {
		switch evalSymlinks(t, wt.Path) {
		case repo:
			sawPrimary = true
		case destResolved:
			linkedBranch = wt.Branch
		}
	}
	if !sawPrimary {
		t.Fatalf("ListWT = %+v, want to include primary %q", wts, repo)
	}
	if linkedBranch != "capataz/feature-01-slug" {
		t.Fatalf("linked worktree branch = %q, want %q", linkedBranch, "capataz/feature-01-slug")
	}
}

func TestCurrentBranch(t *testing.T) {
	repo := initRepo(t)
	got, err := CurrentBranch(repo)
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	// The default branch name varies by git config (main/master), so just assert
	// it's a non-empty, non-detached ref.
	if got == "" || got == "HEAD" {
		t.Fatalf("CurrentBranch = %q, want a branch name", got)
	}
}

func TestMergeCleanAndRemoveMerged(t *testing.T) {
	repo := initRepo(t)
	writeCommit(t, repo, "base.txt", "base\n", "add base")

	dest := filepath.Join(t.TempDir(), "wt")
	branch := "capataz/feature-01-slug"
	if err := Add(repo, dest, branch); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Commit a non-conflicting change on the worktree branch.
	writeCommit(t, dest, "new.txt", "hello\n", "add new file")

	if err := Merge(repo, branch); err != nil {
		t.Fatalf("Merge (clean): %v", err)
	}
	// The merged file should now be present in the primary checkout.
	if _, err := os.Stat(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatalf("merged file not present in primary: %v", err)
	}

	if err := RemoveMerged(repo, dest, branch); err != nil {
		t.Fatalf("RemoveMerged: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("worktree dir still present after RemoveMerged: stat err=%v", err)
	}
	// The branch should be gone.
	if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", branch).Run(); err == nil {
		t.Fatalf("branch %q still exists after RemoveMerged", branch)
	}
}

func TestMergeConflict(t *testing.T) {
	repo := initRepo(t)
	writeCommit(t, repo, "shared.txt", "base\n", "add shared")

	dest := filepath.Join(t.TempDir(), "wt")
	branch := "capataz/feature-02-slug"
	if err := Add(repo, dest, branch); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Divergent edits to the same line -> a merge conflict.
	writeCommit(t, dest, "shared.txt", "worktree change\n", "edit on branch")
	writeCommit(t, repo, "shared.txt", "primary change\n", "edit on base")

	err := Merge(repo, branch)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Merge (conflict) = %v, want ErrConflict", err)
	}
	// The repo should be left mid-merge with unmerged paths.
	out, gerr := exec.Command("git", "-C", repo, "diff", "--name-only", "--diff-filter=U").Output()
	if gerr != nil {
		t.Fatalf("diff: %v", gerr)
	}
	if len(out) == 0 {
		t.Fatal("expected unmerged paths after a conflicting Merge, found none")
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
