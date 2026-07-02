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

// TestDispatchableSet checks the parallel-dispatch guard: only Bucket "todo"
// issues are dispatchable; resolved/blocked/claimed are skipped with a
// bucket-labeled reason, so dispatchParallel never spawns an agent on terminal
// or blocked work (the live-shakeout finding).
func TestDispatchableSet(t *testing.T) {
	sel := []issues.Issue{
		{Num: "01", Status: issues.StatusReadyForAgent},
		{Num: "02", Status: issues.StatusResolved},
		{Num: "03", Status: issues.StatusReadyForAgent, Blocked: true},
		{Num: "04", Status: issues.StatusClaimed},
		{Num: "05", Status: ""}, // empty -> todo -> dispatchable
	}
	dispatchable, skipped := dispatchableSet(sel)

	var gotNums []string
	for _, iss := range dispatchable {
		gotNums = append(gotNums, iss.Num)
	}
	if strings.Join(gotNums, ",") != "01,05" {
		t.Errorf("dispatchable = %v, want [01 05]", gotNums)
	}

	wantSkipped := []string{"02 (done)", "03 (blocked)", "04 (doing)"}
	if strings.Join(skipped, ",") != strings.Join(wantSkipped, ",") {
		t.Errorf("skipped = %v, want %v", skipped, wantSkipped)
	}
}

// TestDispatchableSetAllSkipped confirms an all-terminal/blocked selection
// yields nothing to dispatch.
func TestDispatchableSetAllSkipped(t *testing.T) {
	sel := []issues.Issue{
		{Num: "01", Status: issues.StatusResolved},
		{Num: "02", Status: issues.StatusReadyForAgent, Blocked: true},
	}
	dispatchable, skipped := dispatchableSet(sel)
	if len(dispatchable) != 0 {
		t.Errorf("dispatchable = %v, want none", dispatchable)
	}
	if len(skipped) != 2 {
		t.Errorf("skipped = %v, want 2 entries", skipped)
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
