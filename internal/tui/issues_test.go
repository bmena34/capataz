package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bmena34/capataz/internal/issues"
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

// TestIssueBadgePartsLabelsFromTable checks the badge label comes from the
// shared issues vocabulary table (not an independent list) for known statuses,
// and that an unknown status falls back to uppercasing while a known one always
// gets a non-default color.
func TestIssueBadgePartsLabelsFromTable(t *testing.T) {
	for _, status := range []string{
		issues.StatusReadyForAgent,
		issues.StatusClaimed,
		issues.StatusResolved,
		issues.StatusWontfix,
		issues.StatusNeedsInfo,
		issues.StatusNeedsTriage,
		issues.StatusReadyForHuman,
	} {
		_, wantLabel, known := issues.StatusInfo(status)
		if !known {
			t.Fatalf("status %q missing from vocabulary table", status)
		}
		gotLabel, _ := issueBadgeParts(status)
		if gotLabel != wantLabel {
			t.Errorf("issueBadgeParts(%q) label = %q, want %q (from table)", status, gotLabel, wantLabel)
		}
	}

	// Empty status renders the placeholder.
	if label, _ := issueBadgeParts(""); label != "—" {
		t.Errorf("issueBadgeParts(\"\") label = %q, want %q", label, "—")
	}

	// Unknown status still renders via the fallback (uppercased), never panics,
	// and is not in the table.
	if _, _, known := issues.StatusInfo("mid-review"); known {
		t.Fatalf("unknown status unexpectedly in table")
	}
	if label, _ := issueBadgeParts("mid-review"); label != "MID-REVIEW" {
		t.Errorf("issueBadgeParts(unknown) label = %q, want %q", label, "MID-REVIEW")
	}
}
