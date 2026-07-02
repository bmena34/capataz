package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// writeIssue creates repo/.scratch/<feature>/issues/<name> with body.
func writeIssue(t *testing.T, repo, feature, name, body string) string {
	t.Helper()
	dir := filepath.Join(repo, scratchDir, feature, "issues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestScanMissingScratch(t *testing.T) {
	feats, err := Scan(t.TempDir())
	if err != nil {
		t.Fatalf("want nil error for missing .scratch, got %v", err)
	}
	if feats != nil {
		t.Fatalf("want nil features, got %v", feats)
	}
}

func TestScanEmptyRepoRoot(t *testing.T) {
	if feats, err := Scan(""); err != nil || feats != nil {
		t.Fatalf("Scan(\"\") = %v, %v; want nil, nil", feats, err)
	}
}

func TestParseHeadersAndTitle(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "login", "01-add-form.md", `# Add the login form

Status: ready-for-agent
Type: task
Blocked by: None

## What to build

A form.
`)
	feats, err := Scan(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(feats) != 1 || len(feats[0].Issues) != 1 {
		t.Fatalf("want 1 feature with 1 issue, got %+v", feats)
	}
	iss := feats[0].Issues[0]
	if iss.Feature != "login" || iss.Num != "01" || iss.Slug != "add-form" {
		t.Errorf("filename parse wrong: %+v", iss)
	}
	if iss.Title != "Add the login form" {
		t.Errorf("title = %q", iss.Title)
	}
	if iss.Status != "ready-for-agent" || iss.Type != "task" {
		t.Errorf("headers wrong: status=%q type=%q", iss.Status, iss.Type)
	}
	if len(iss.BlockedBy) != 0 {
		t.Errorf("BlockedBy should be empty for None, got %v", iss.BlockedBy)
	}
	if iss.Blocked {
		t.Errorf("issue with no blockers should not be Blocked")
	}
}

func TestTitleFallsBackToSlug(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "feat", "02-wire_up-thing.md", "Status: claimed\n\nno heading here\n")
	feats, _ := Scan(repo)
	iss := feats[0].Issues[0]
	if iss.Title != "Wire up thing" {
		t.Errorf("slug title = %q, want %q", iss.Title, "Wire up thing")
	}
}

func TestStatusInCommentsIgnored(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "feat", "01-x.md", `# X

Status: ready-for-agent

## Comments

Status: this-should-be-ignored
`)
	feats, _ := Scan(repo)
	if got := feats[0].Issues[0].Status; got != "ready-for-agent" {
		t.Errorf("status = %q, want ready-for-agent (comment must be ignored)", got)
	}
}

func TestBlockedByUnresolvedBlocker(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "01-base.md", "# Base\n\nStatus: ready-for-agent\n")
	writeIssue(t, repo, "f", "02-dep.md", "# Dep\n\nStatus: ready-for-agent\nBlocked by: 01\n")
	feats, _ := Scan(repo)
	byNum := map[string]Issue{}
	for _, iss := range feats[0].Issues {
		byNum[iss.Num] = iss
	}
	if byNum["02"].Blocked != true {
		t.Errorf("02 should be blocked by unresolved 01")
	}
	if byNum["01"].Blocked {
		t.Errorf("01 has no blockers")
	}
}

func TestBlockedByResolvedBlocker(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "01-base.md", "# Base\n\nStatus: resolved\n")
	writeIssue(t, repo, "f", "02-dep.md", "# Dep\n\nStatus: ready-for-agent\nBlocked by: 01\n")
	feats, _ := Scan(repo)
	for _, iss := range feats[0].Issues {
		if iss.Num == "02" && iss.Blocked {
			t.Errorf("02 should be unblocked once 01 is resolved")
		}
	}
}

func TestIssuesSortedByNum(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "03-c.md", "# C\n")
	writeIssue(t, repo, "f", "01-a.md", "# A\n")
	writeIssue(t, repo, "f", "02-b.md", "# B\n")
	feats, _ := Scan(repo)
	var got []string
	for _, iss := range feats[0].Issues {
		got = append(got, iss.Num)
	}
	if strings.Join(got, "") != "010203" {
		t.Errorf("issues not sorted by num: %v", got)
	}
}

func TestSetStatusReplacesExisting(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: ready-for-agent\nType: task\n\n## Body\n")
	if err := SetStatus(path, "claimed"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "Status: claimed") || strings.Contains(string(data), "ready-for-agent") {
		t.Errorf("status not replaced:\n%s", data)
	}
	if !strings.Contains(string(data), "Type: task") {
		t.Errorf("SetStatus clobbered other headers:\n%s", data)
	}
	if !strings.Contains(string(data), "## Body") {
		t.Errorf("SetStatus lost body:\n%s", data)
	}
}

func TestSetStatusInsertsWhenMissing(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", "# X\n\n## Body\n\ntext\n")
	if err := SetStatus(path, "claimed"); err != nil {
		t.Fatal(err)
	}
	feats, _ := Scan(repo)
	if got := feats[0].Issues[0].Status; got != "claimed" {
		t.Errorf("after insert, parsed status = %q, want claimed", got)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "## Body") {
		t.Errorf("SetStatus lost body:\n%s", data)
	}
}

func TestCreateNumbersAndParses(t *testing.T) {
	repo := t.TempDir()
	// Seed an existing 01 so create picks 02.
	writeIssue(t, repo, "feat", "01-existing.md", "# Existing\n\nStatus: resolved\n")
	if err := create("feat", "Add the Retry Logic!", []string{"--repo", repo, "--body", "do it"}); err != nil {
		t.Fatal(err)
	}
	feats, err := Scan(repo)
	if err != nil {
		t.Fatal(err)
	}
	var got *Issue
	for i := range feats[0].Issues {
		if feats[0].Issues[i].Num == "02" {
			got = &feats[0].Issues[i]
		}
	}
	if got == nil {
		t.Fatalf("no issue numbered 02 created: %+v", feats[0].Issues)
	}
	if got.Slug != "add-the-retry-logic" {
		t.Errorf("slug = %q, want add-the-retry-logic", got.Slug)
	}
	if got.Status != "ready-for-agent" {
		t.Errorf("status = %q, want ready-for-agent", got.Status)
	}
	if got.Title != "Add the Retry Logic!" {
		t.Errorf("title = %q", got.Title)
	}
}

func TestCreateConcurrentNoDuplicateNumbers(t *testing.T) {
	repo := t.TempDir()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Distinct titles so filenames differ — the only thing preventing a
			// duplicate NUMBER is lockFeature's serialization.
			_ = create("feat", fmt.Sprintf("finding number %d", i), []string{"--repo", repo})
		}(i)
	}
	wg.Wait()

	feats, err := Scan(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(feats) != 1 {
		t.Fatalf("want 1 feature, got %d", len(feats))
	}
	seen := map[string]bool{}
	for _, iss := range feats[0].Issues {
		if seen[iss.Num] {
			t.Errorf("duplicate issue number %q — lockFeature did not serialize", iss.Num)
		}
		seen[iss.Num] = true
	}
	if len(feats[0].Issues) != n {
		t.Errorf("want %d issues, got %d", n, len(feats[0].Issues))
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Add the Retry Logic!": "add-the-retry-logic",
		"  Weird__name  ":      "weird-name",
		"":                     "issue",
		"!!!":                  "issue",
		"CamelCase and spaces": "camelcase-and-spaces",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBucket(t *testing.T) {
	cases := []struct {
		iss  Issue
		want string
	}{
		{Issue{Status: "ready-for-agent"}, "todo"},
		{Issue{Status: "needs-triage"}, "todo"},
		{Issue{Status: "claimed"}, "doing"},
		{Issue{Status: "resolved"}, "done"},
		{Issue{Status: "wontfix"}, "done"},
		{Issue{Status: "ready-for-agent", Blocked: true}, "blocked"},
		{Issue{Status: "resolved", Blocked: true}, "done"}, // done wins over blocked
		{Issue{Status: "claimed", Blocked: true}, "blocked"},
	}
	for _, c := range cases {
		if got := Bucket(c.iss); got != c.want {
			t.Errorf("Bucket(%+v) = %q, want %q", c.iss, got, c.want)
		}
	}
}

func TestParseTouches(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"internal/issues/issues.go, internal/tui/", []string{"internal/issues/issues.go", "internal/tui"}},
		{"  foo.go ,, bar.go  ", []string{"foo.go", "bar.go"}},
		{"./internal/tui/", []string{"internal/tui"}},
		{"None", nil},
		{"", nil},
		{"   ", nil},
	}
	for _, c := range cases {
		got := parsePaths(c.in)
		if strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("parsePaths(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestTouchesHeaderParsed(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: ready-for-agent\nTouches: internal/tui/, foo.go\n")
	feats, _ := Scan(repo)
	got := feats[0].Issues[0].Touches
	if strings.Join(got, "|") != "internal/tui|foo.go" {
		t.Errorf("Touches = %v", got)
	}
}

func TestPathsOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"internal/tui", "internal/tui", true},                        // equal
		{"internal/tui", "internal/tui/issues.go", true},              // dir prefixes file
		{"internal/tui/issues.go", "internal/tui", true},              // symmetric
		{"internal/t", "internal/tui", false},                         // NOT a segment prefix
		{"internal/tui", "internal/tui2", false},                      // sibling, not prefix
		{"internal/issues/issues.go", "internal/tui/issues.go", false}, // different dirs
		{"internal/tui/", "internal/tui/issues.go", true},             // trailing slash normalized
		{"./foo.go", "foo.go", true},                                  // leading ./ normalized
	}
	for _, c := range cases {
		if got := PathsOverlap(c.a, c.b); got != c.want {
			t.Errorf("PathsOverlap(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestParallelSafe(t *testing.T) {
	// 01 & 03 touch disjoint files, no blocking edge -> same wave.
	// 02 overlaps 01 (both touch internal/tui) -> own wave.
	// 04 has no Touches -> singleton (unknown scope overlaps everything).
	// 05 is claimed (not "todo") -> excluded entirely.
	// 06 blocked-by 01 with disjoint files -> still cannot share 01's wave.
	list := []Issue{
		{Num: "01", Status: "ready-for-agent", Touches: []string{"internal/tui/a.go"}},
		{Num: "02", Status: "ready-for-agent", Touches: []string{"internal/tui/b.go", "internal/tui"}},
		{Num: "03", Status: "ready-for-agent", Touches: []string{"internal/store/db.go"}},
		{Num: "04", Status: "ready-for-agent"},
		{Num: "05", Status: "claimed", Touches: []string{"internal/x/y.go"}},
		{Num: "06", Status: "ready-for-agent", Touches: []string{"cmd/main.go"}, BlockedBy: []string{"01"}},
	}
	waves := ParallelSafe(list)

	// 05 (claimed) must not appear anywhere.
	for _, w := range waves {
		for _, iss := range w {
			if iss.Num == "05" {
				t.Fatalf("claimed issue 05 must not appear in waves")
			}
		}
	}

	// The first wave should pair 01 with 03 (disjoint, no edge) — but NOT 02 (overlap)
	// and NOT 06 (blocking edge to 01).
	want := map[string]bool{"01": true, "03": true}
	got := map[string]bool{}
	for _, iss := range waves[0] {
		got[iss.Num] = true
	}
	if len(got) != len(want) {
		t.Fatalf("wave[0] = %v, want {01,03}", got)
	}
	for n := range want {
		if !got[n] {
			t.Errorf("wave[0] missing %s: %v", n, got)
		}
	}

	// 02 (overlap with 01) and 06 (blocked by 01) must each be in a wave without 01.
	for _, n := range []string{"02", "06"} {
		for _, w := range waves {
			hasN, has01 := false, false
			for _, iss := range w {
				if iss.Num == n {
					hasN = true
				}
				if iss.Num == "01" {
					has01 = true
				}
			}
			if hasN && has01 {
				t.Errorf("%s must not share a wave with 01", n)
			}
		}
	}
}

func TestParallelSafeEmptyTouchesNeverPairs(t *testing.T) {
	// Two ready issues, one with unknown scope — they must never be paired.
	list := []Issue{
		{Num: "01", Status: "ready-for-agent"},
		{Num: "02", Status: "ready-for-agent", Touches: []string{"internal/store/db.go"}},
	}
	waves := ParallelSafe(list)
	for _, w := range waves {
		if len(w) > 1 {
			t.Errorf("empty-Touches issue must not be parallel-safe with others: %v", w)
		}
	}
}

func TestMutuallyParallelSafe(t *testing.T) {
	cases := []struct {
		name string
		set  []Issue
		want bool
	}{
		{"disjoint pair", []Issue{
			{Num: "01", Touches: []string{"internal/tui/a.go"}},
			{Num: "02", Touches: []string{"internal/store/db.go"}},
		}, true},
		{"overlapping pair", []Issue{
			{Num: "01", Touches: []string{"internal/tui"}},
			{Num: "02", Touches: []string{"internal/tui/a.go"}},
		}, false},
		{"blocking edge", []Issue{
			{Num: "01", Touches: []string{"a.go"}},
			{Num: "02", Touches: []string{"b.go"}, BlockedBy: []string{"01"}},
		}, false},
		{"unknown scope", []Issue{
			{Num: "01", Touches: []string{"a.go"}},
			{Num: "02"}, // no Touches -> unsafe
		}, false},
		{"single issue trivially safe", []Issue{
			{Num: "01", Touches: []string{"a.go"}},
		}, true},
		{"empty set trivially safe", nil, true},
		{"three disjoint", []Issue{
			{Num: "01", Touches: []string{"a.go"}},
			{Num: "02", Touches: []string{"b.go"}},
			{Num: "03", Touches: []string{"c.go"}},
		}, true},
		{"three with one overlap", []Issue{
			{Num: "01", Touches: []string{"a.go"}},
			{Num: "02", Touches: []string{"b.go"}},
			{Num: "03", Touches: []string{"a.go"}}, // overlaps 01
		}, false},
	}
	for _, c := range cases {
		if got := MutuallyParallelSafe(c.set); got != c.want {
			t.Errorf("%s: MutuallyParallelSafe = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNonNumberedFile(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "notes.md", "# Some notes\n\nStatus: ready-for-agent\n")
	feats, _ := Scan(repo)
	iss := feats[0].Issues[0]
	if iss.Num != "" || iss.Slug != "notes" {
		t.Errorf("unnumbered file parse wrong: num=%q slug=%q", iss.Num, iss.Slug)
	}
}

func TestDispatchBranchAndMatch(t *testing.T) {
	list := []Issue{
		{Feature: "login", Num: "01", Slug: "form", Status: "resolved"},
		{Feature: "login", Num: "02", Slug: "cookies", Status: "claimed"},
		{Feature: "billing", Num: "", Slug: "notes", Status: "ready-for-agent"},
	}

	// DispatchBranch is the branch shape dispatch cuts.
	if got := DispatchBranch(list[0]); got != "capataz/login-01-form" {
		t.Fatalf("DispatchBranch = %q, want %q", got, "capataz/login-01-form")
	}
	if got := DispatchBranch(list[2]); got != "capataz/billing----notes" {
		t.Fatalf("DispatchBranch (unnumbered) = %q, want %q", got, "capataz/billing----notes")
	}

	// MatchBranch round-trips DispatchBranch.
	if iss, ok := MatchBranch("capataz/login-01-form", list); !ok || iss.Num != "01" {
		t.Fatalf("MatchBranch(login-01) = %+v, %v", iss, ok)
	}
	// A branch without the capataz/ prefix never matches.
	if _, ok := MatchBranch("main", list); ok {
		t.Fatal("MatchBranch(main) matched, want no match")
	}
	// A capataz/ branch with no backing issue doesn't match.
	if _, ok := MatchBranch("capataz/login-99-gone", list); ok {
		t.Fatal("MatchBranch(unknown) matched, want no match")
	}
}
