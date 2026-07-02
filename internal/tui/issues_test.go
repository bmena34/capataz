package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmena34/capataz/internal/paths"
)

func TestRepoWorktreeIDSingleSegmentAndStable(t *testing.T) {
	id := repoWorktreeID("/Users/x/personal-code/capataz")
	if strings.ContainsRune(id, filepath.Separator) {
		t.Errorf("repoWorktreeID must be a single path segment, got %q", id)
	}
	if !strings.HasPrefix(id, "capataz-") {
		t.Errorf("repoWorktreeID should carry the basename prefix, got %q", id)
	}
	// Stable for the same input.
	if id != repoWorktreeID("/Users/x/personal-code/capataz") {
		t.Errorf("repoWorktreeID not stable for the same repo")
	}
}

func TestRepoWorktreeIDDisambiguatesSameBasename(t *testing.T) {
	a := repoWorktreeID("/Users/x/a/capataz")
	b := repoWorktreeID("/Users/x/b/capataz")
	if a == b {
		t.Errorf("same-basename repos must not collide: %q == %q", a, b)
	}
}

func TestWorktreeDispatchPathUnderWorktreesDir(t *testing.T) {
	repo := "/Users/x/proj"
	dir := filepath.Join(paths.WorktreesDir(), repoWorktreeID(repo), "login-01-add-form")
	if !strings.HasPrefix(dir, paths.WorktreesDir()) {
		t.Errorf("worktree dir %q not under WorktreesDir %q", dir, paths.WorktreesDir())
	}
	if filepath.Base(dir) != "login-01-add-form" {
		t.Errorf("worktree leaf = %q, want login-01-add-form", filepath.Base(dir))
	}
}

func TestWorktreeDispatchPromptShape(t *testing.T) {
	got := worktreeDispatchPrompt("/abs/.scratch/login/issues/01-add-form.md", "capataz/login-01-add-form")
	for _, want := range []string{
		"/capataz-work skill",
		"/abs/.scratch/login/issues/01-add-form.md",
		"isolated git worktree on branch capataz/login-01-add-form",
		"do not merge",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
}
