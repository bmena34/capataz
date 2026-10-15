// Package worktree resolves a repo's primary checkout. The per-repo issue
// backlog lives in the main working tree (.scratch/<feature>/issues/*.md), so
// anything reading or writing the backlog from inside a linked worktree must
// first resolve back to the primary checkout. For a normal (non-worktree) repo
// that primary is the checkout itself.
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
