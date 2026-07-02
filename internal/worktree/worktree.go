// Package worktree wraps the handful of `git worktree` operations capataz needs
// to run dispatched sessions in isolated checkouts. It shells out to git the
// same way internal/tui/tmux.go shells out to tmux — no new dependencies.
//
// The key primitive is PrimaryCheckout: the per-repo issue backlog lives in the
// main working tree (.scratch/<feature>/issues/*.md), so anything reading or
// writing the backlog from inside a linked worktree must first resolve back to
// the primary checkout. For a normal (non-worktree) repo that primary is the
// checkout itself, so callers using PrimaryCheckout are behavior-preserving.
package worktree

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// ErrConflict is returned by Merge when the merge left conflicted (unmerged)
// files in the working tree. The caller can detect it (errors.Is) to dispatch a
// fix-conflict session instead of treating the merge as a hard failure.
var ErrConflict = errors.New("merge conflict")

// WT is one worktree entry: its checkout path and the branch it has checked out
// (empty for a detached HEAD).
type WT struct {
	Path   string
	Branch string
}

// PrimaryCheckout returns the main working tree for whatever repo or linked
// worktree dir sits inside. `git worktree list --porcelain` always emits the
// primary checkout first, so the first `worktree <path>` line is it. Any git
// error (dir isn't a repo, git missing) is returned so callers can decide how
// to fall back — this function never silently returns dir on failure.
func PrimaryCheckout(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return "", fmt.Errorf("git worktree list: %w", err)
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(ln, "worktree "); ok {
			return strings.TrimSpace(path), nil
		}
	}
	return "", fmt.Errorf("git worktree list: no worktree entry for %s", dir)
}

// Add creates a new linked worktree at dest on a new branch cut from repoDir's
// current HEAD: `git -C <repoDir> worktree add -b <branch> <dest>`.
func Add(repoDir, dest, branch string) error {
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "add", "-b", branch, dest).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Remove tears down the linked worktree at dest:
// `git -C <repoDir> worktree remove --force <dest>`. --force so a worktree with
// a dirty or otherwise-unclean tree is still removed.
func Remove(repoDir, dest string) error {
	if out, err := exec.Command("git", "-C", repoDir, "worktree", "remove", "--force", dest).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// List returns every worktree path for repoDir's repo (primary first), parsed
// from `git worktree list --porcelain`.
func List(repoDir string) ([]string, error) {
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var paths []string
	for _, ln := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(ln, "worktree "); ok {
			paths = append(paths, strings.TrimSpace(path))
		}
	}
	return paths, nil
}

// ListWT is like List but returns the branch each worktree has checked out
// alongside its path, parsed from the `worktree <path>` and
// `branch refs/heads/<name>` lines of `git worktree list --porcelain`. Each
// porcelain record starts with a `worktree` line and ends at a blank line; a
// detached-HEAD worktree has no `branch` line and yields an empty Branch.
func ListWT(repoDir string) ([]WT, error) {
	out, err := exec.Command("git", "-C", repoDir, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	var wts []WT
	var cur *WT
	for _, ln := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(ln, "worktree "); ok {
			wts = append(wts, WT{Path: strings.TrimSpace(path)})
			cur = &wts[len(wts)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if ref, ok := strings.CutPrefix(ln, "branch "); ok {
			// e.g. "branch refs/heads/capataz/foo-01-slug" -> "capataz/foo-01-slug"
			cur.Branch = strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
		}
	}
	return wts, nil
}

// Merge merges branch into primaryDir's current branch with a merge commit:
// `git -C primaryDir merge --no-ff <branch>`. On a clean merge it returns nil.
// When the merge fails, it inspects the unmerged (conflicted) files; if any
// exist it returns ErrConflict (leaving the repo mid-merge so a fix-conflict
// agent can resolve and commit it), otherwise it returns the raw git error.
func Merge(primaryDir, branch string) error {
	if out, err := exec.Command("git", "-C", primaryDir, "merge", "--no-ff", branch).CombinedOutput(); err != nil {
		if conflicted(primaryDir) {
			return ErrConflict
		}
		return fmt.Errorf("git merge %s: %s", branch, strings.TrimSpace(string(out)))
	}
	return nil
}

// conflicted reports whether primaryDir has unmerged (conflicted) paths, per
// `git diff --name-only --diff-filter=U`.
func conflicted(primaryDir string) bool {
	out, err := exec.Command("git", "-C", primaryDir, "diff", "--name-only", "--diff-filter=U").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// RemoveMerged tears down a worktree whose branch has been cleanly merged: it
// removes the linked worktree at path, then deletes the now-merged branch with
// `git branch -d` (a safe delete). A branch-delete failure is returned softly as
// nil-ish — reported to the caller as an error only if the worktree removal
// itself fails; the branch delete is best-effort so a lingering merged branch
// doesn't block the batch.
func RemoveMerged(primaryDir, path, branch string) error {
	if out, err := exec.Command("git", "-C", primaryDir, "worktree", "remove", "--force", path).CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s", strings.TrimSpace(string(out)))
	}
	// Best-effort: -d only deletes a fully-merged branch; ignore failures.
	_ = exec.Command("git", "-C", primaryDir, "branch", "-d", branch).Run()
	return nil
}

// CurrentBranch returns the abbreviated current branch of dir
// (`git rev-parse --abbrev-ref HEAD`), e.g. "main". A detached HEAD yields
// "HEAD".
func CurrentBranch(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
