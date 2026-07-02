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
	"fmt"
	"os/exec"
	"strings"
)

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
