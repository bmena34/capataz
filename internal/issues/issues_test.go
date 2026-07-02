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

func TestNonNumberedFile(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "notes.md", "# Some notes\n\nStatus: ready-for-agent\n")
	feats, _ := Scan(repo)
	iss := feats[0].Issues[0]
	if iss.Num != "" || iss.Slug != "notes" {
		t.Errorf("unnumbered file parse wrong: num=%q slug=%q", iss.Num, iss.Slug)
	}
}
