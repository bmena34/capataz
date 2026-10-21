package issues

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestBlockedBySelfReferenceNotBlocked(t *testing.T) {
	repo := t.TempDir()
	// An issue that lists its own number must not be marked blocked by itself.
	writeIssue(t, repo, "f", "02-selfref.md", "# Self\n\nStatus: ready-for-agent\nBlocked by: 02\n")
	feats, _ := Scan(repo)
	if feats[0].Issues[0].Blocked {
		t.Errorf("issue listing its own number should not be blocked")
	}
}

func TestBlockedByDuplicateNumberAllMustBeDone(t *testing.T) {
	repo := t.TempDir()
	// Two files share number 01: one resolved, one not. A sibling blocked by 01
	// must stay blocked, not silently resolve against the resolved copy.
	writeIssue(t, repo, "f", "01-a.md", "# A\n\nStatus: resolved\n")
	writeIssue(t, repo, "f", "01-b.md", "# B\n\nStatus: ready-for-agent\n")
	writeIssue(t, repo, "f", "02-dep.md", "# Dep\n\nStatus: ready-for-agent\nBlocked by: 01\n")
	feats, _ := Scan(repo)
	var dep *Issue
	for i := range feats[0].Issues {
		if feats[0].Issues[i].Slug == "dep" {
			dep = &feats[0].Issues[i]
		}
	}
	if dep == nil || !dep.Blocked {
		t.Errorf("02 should stay blocked while a file sharing blocker number 01 is not resolved")
	}
}

func TestBlockedByDuplicateNumberAllDoneUnblocks(t *testing.T) {
	repo := t.TempDir()
	// Both 01 files done (resolved + wontfix) -> the 01 blocker is satisfied.
	writeIssue(t, repo, "f", "01-a.md", "# A\n\nStatus: resolved\n")
	writeIssue(t, repo, "f", "01-b.md", "# B\n\nStatus: wontfix\n")
	writeIssue(t, repo, "f", "02-dep.md", "# Dep\n\nStatus: ready-for-agent\nBlocked by: 01\n")
	feats, _ := Scan(repo)
	for _, iss := range feats[0].Issues {
		if iss.Slug == "dep" && iss.Blocked {
			t.Errorf("02 should be unblocked once every file numbered 01 is done")
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

// claimStatus reads back the parsed Status of an issue file for claim tests.
func claimStatus(t *testing.T, path string) string {
	t.Helper()
	return parseIssue(path, "").Status
}

func TestClaimReadyForAgentSucceeds(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: ready-for-agent\nType: task\n\n## Body\n")
	if err := claimCLI(path, false); err != nil {
		t.Fatalf("claim ready-for-agent should succeed, got %v", err)
	}
	if got := claimStatus(t, path); got != StatusClaimed {
		t.Errorf("status = %q, want %q", got, StatusClaimed)
	}
}

func TestClaimAlreadyClaimedErrors(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: claimed\n")
	err := claimCLI(path, false)
	if err == nil {
		t.Fatal("claim on an already-claimed issue should error")
	}
	if !strings.Contains(err.Error(), "already claimed") {
		t.Errorf("error = %q, want it to mention 'already claimed'", err)
	}
	// The status must be untouched by a rejected claim.
	if got := claimStatus(t, path); got != StatusClaimed {
		t.Errorf("rejected claim changed status to %q", got)
	}
}

func TestClaimResolvedErrors(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: resolved\n")
	err := claimCLI(path, false)
	if err == nil {
		t.Fatal("claim on a resolved issue should error")
	}
	if !strings.Contains(err.Error(), "not claimable") || !strings.Contains(err.Error(), StatusResolved) {
		t.Errorf("error = %q, want 'not claimable: resolved'", err)
	}
	if got := claimStatus(t, path); got != StatusResolved {
		t.Errorf("rejected claim changed status to %q", got)
	}
}

func TestClaimForceOverridesStateCheck(t *testing.T) {
	repo := t.TempDir()
	// --force must claim an issue in any state (here already claimed, and resolved).
	for _, start := range []string{StatusClaimed, StatusResolved, StatusWontfix} {
		path := writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: "+start+"\n")
		if err := claimCLI(path, true); err != nil {
			t.Fatalf("claim --force on %q should succeed, got %v", start, err)
		}
		if got := claimStatus(t, path); got != StatusClaimed {
			t.Errorf("after --force from %q, status = %q, want claimed", start, got)
		}
	}
}

// TestClaimConcurrentSingleWinner is the AC #3 guard: N goroutines race to claim
// one ready-for-agent issue; the per-file lock + in-lock re-read must yield
// exactly one winner and (N-1) "already claimed" errors. Run with -race.
func TestClaimConcurrentSingleWinner(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-x.md", "# X\n\nStatus: ready-for-agent\n")

	const n = 16
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		wins    int
		claimed int // count of "already claimed" errors
		others  []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // release all goroutines together to maximize contention
			err := claimCLI(path, false)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case strings.Contains(err.Error(), "already claimed"):
				claimed++
			default:
				others = append(others, err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if wins != 1 {
		t.Errorf("want exactly 1 winner, got %d", wins)
	}
	if claimed != n-1 {
		t.Errorf("want %d 'already claimed' errors, got %d", n-1, claimed)
	}
	if len(others) != 0 {
		t.Errorf("unexpected errors: %v", others)
	}
	if got := claimStatus(t, path); got != StatusClaimed {
		t.Errorf("final status = %q, want claimed", got)
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

func TestSlugToTitle(t *testing.T) {
	cases := map[string]string{
		"add-login-form": "Add login form",
		"single":         "Single",
		"under_score":    "Under score",
		"":               "(untitled)",
		// Non-ASCII first rune must uppercase correctly, not corrupt the byte.
		"éclair-au-chocolat": "Éclair au chocolat",
		"über-menu":          "Über menu",
	}
	for in, want := range cases {
		if got := slugToTitle(in); got != want {
			t.Errorf("slugToTitle(%q) = %q, want %q", in, got, want)
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
		{Issue{Status: "mid-review"}, "todo"},                   // unknown status: tolerant default
		{Issue{Status: "mid-review", Blocked: true}, "blocked"}, // unknown + blocked still blocks
		{Issue{Status: ""}, "todo"},                             // absent status: todo
	}
	for _, c := range cases {
		if got := Bucket(c.iss); got != c.want {
			t.Errorf("Bucket(%+v) = %q, want %q", c.iss, got, c.want)
		}
	}
}

// TestStatusVocabularySingleSource is the AC #4 guard: a status's bucket and
// display label both flow from the single vocabulary table (the `statuses` map,
// read via StatusInfo/Bucket), so adding a status is a one-place change. It also
// pins that an unknown status stays out of the table and falls through to the
// tolerant defaults — Status is not a closed enum.
func TestStatusVocabularySingleSource(t *testing.T) {
	cases := []struct {
		status     string
		wantBucket string
		wantLabel  string
	}{
		{StatusReadyForAgent, BucketTodo, "READY"},
		{StatusNeedsTriage, BucketTodo, "TRIAGE"},
		{StatusReadyForHuman, BucketTodo, "HUMAN"},
		{StatusNeedsInfo, BucketTodo, "NEEDS-INFO"},
		{StatusClaimed, BucketDoing, "CLAIMED"},
		{StatusAsking, BucketDoing, "ASKING"},
		{StatusResolved, BucketDone, "DONE"},
		{StatusDone, BucketDone, "DONE"},
		{StatusClosed, BucketDone, "DONE"},
		{StatusWontfix, BucketDone, "WONTFIX"},
	}
	for _, c := range cases {
		bucket, label, known := StatusInfo(c.status)
		if !known {
			t.Errorf("StatusInfo(%q): known=false, want true (must be in the table)", c.status)
			continue
		}
		if label != c.wantLabel {
			t.Errorf("StatusInfo(%q) label = %q, want %q", c.status, label, c.wantLabel)
		}
		if bucket != c.wantBucket {
			t.Errorf("StatusInfo(%q) bucket = %q, want %q", c.status, bucket, c.wantBucket)
		}
		// The bucket a status carries in the table must match what Bucket() derives
		// for an unblocked issue with that status — one source, no drift.
		if got := Bucket(Issue{Status: c.status}); got != c.wantBucket {
			t.Errorf("Bucket(status=%q) = %q, want %q (table drift)", c.status, got, c.wantBucket)
		}
	}

	// An unknown status is absent from the table and Done treats it as open.
	if _, _, known := StatusInfo("mid-review"); known {
		t.Errorf("StatusInfo(unknown) known=true, want false — table must stay open")
	}
	if Done("mid-review") {
		t.Errorf("Done(unknown) = true, want false")
	}
	// Case/space tolerance: lookups normalize before hitting the table.
	if _, _, known := StatusInfo("  Ready-For-Agent  "); !known {
		t.Errorf("StatusInfo is not case/space tolerant")
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
		{"internal/tui", "internal/tui", true},                         // equal
		{"internal/tui", "internal/tui/issues.go", true},               // dir prefixes file
		{"internal/tui/issues.go", "internal/tui", true},               // symmetric
		{"internal/t", "internal/tui", false},                          // NOT a segment prefix
		{"internal/tui", "internal/tui2", false},                       // sibling, not prefix
		{"internal/issues/issues.go", "internal/tui/issues.go", false}, // different dirs
		{"internal/tui/", "internal/tui/issues.go", true},              // trailing slash normalized
		{"./foo.go", "foo.go", true},                                   // leading ./ normalized
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

func TestDispatchable(t *testing.T) {
	cases := []struct {
		name string
		iss  Issue
		want bool
	}{
		{"ready-for-agent", Issue{Status: StatusReadyForAgent}, true},
		{"needs-triage still todo", Issue{Status: StatusNeedsTriage}, true},
		{"unknown status falls through to todo", Issue{Status: "mid-review"}, true},
		{"empty status", Issue{Status: ""}, true},
		{"claimed (doing) not dispatchable", Issue{Status: StatusClaimed}, false},
		{"resolved (done) not dispatchable", Issue{Status: StatusResolved}, false},
		{"wontfix (done) not dispatchable", Issue{Status: StatusWontfix}, false},
		{"blocked not dispatchable", Issue{Status: StatusReadyForAgent, Blocked: true}, false},
	}
	for _, c := range cases {
		if got := Dispatchable(c.iss); got != c.want {
			t.Errorf("%s: Dispatchable = %v, want %v", c.name, got, c.want)
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

func TestDispatchBranch(t *testing.T) {
	list := []Issue{
		{Feature: "login", Num: "01", Slug: "form", Status: "resolved"},
		{Feature: "login", Num: "02", Slug: "cookies", Status: "claimed"},
		{Feature: "billing", Num: "", Slug: "notes", Status: "ready-for-agent"},
	}

	// DispatchBranch is the branch shape dispatch cuts.
	if got := DispatchBranch(list[0], ""); got != "login-01-form" {
		t.Fatalf("DispatchBranch = %q, want %q", got, "login-01-form")
	}
	if got := DispatchBranch(list[2], "bm/"); got != "bm/billing----notes" {
		t.Fatalf("DispatchBranch (unnumbered) = %q, want %q", got, "bm/billing----notes")
	}

}

func TestScratchModTimeMissingScratch(t *testing.T) {
	got, err := ScratchModTime(t.TempDir())
	if err != nil {
		t.Fatalf("want nil error for missing .scratch, got %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("want zero time for missing .scratch, got %v", got)
	}
}

func TestScratchModTimeEmptyRepoRoot(t *testing.T) {
	if got, err := ScratchModTime(""); err != nil || !got.IsZero() {
		t.Fatalf("ScratchModTime(\"\") = %v, %v; want zero, nil", got, err)
	}
}

// A claim/resolve rewrites an existing issue file in place (SetStatus does
// os.WriteFile), which bumps the file's mtime but not the parent dir's. The
// probe must reflect that, or an agent's edit would never be noticed.
func TestScratchModTimeSeesFileRewrite(t *testing.T) {
	repo := t.TempDir()
	p := writeIssue(t, repo, "f", "01-a.md", "# A\n\nStatus: ready-for-agent\n")

	before, err := ScratchModTime(repo)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate an in-place status rewrite with a strictly newer mtime, without
	// depending on wall-clock/mtime resolution: set the file's mtime forward and
	// leave the containing directories untouched.
	newer := before.Add(2 * time.Second)
	if err := os.Chtimes(p, newer, newer); err != nil {
		t.Fatal(err)
	}

	after, err := ScratchModTime(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !after.After(before) {
		t.Fatalf("probe did not see file rewrite: before=%v after=%v", before, after)
	}
	if !after.Equal(newer) {
		t.Fatalf("probe = %v, want the rewritten file's mtime %v", after, newer)
	}
}

// Adding a new issue file (capataz issue new) must move the probe forward so a
// freshly-filed issue shows up on the next tick.
func TestScratchModTimeSeesNewFile(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "01-a.md", "# A\n\nStatus: ready-for-agent\n")
	before, err := ScratchModTime(repo)
	if err != nil {
		t.Fatal(err)
	}

	p := writeIssue(t, repo, "f", "02-b.md", "# B\n\nStatus: ready-for-agent\n")
	newer := before.Add(2 * time.Second)
	if err := os.Chtimes(p, newer, newer); err != nil {
		t.Fatal(err)
	}

	after, err := ScratchModTime(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !after.After(before) {
		t.Fatalf("probe did not see the new file: before=%v after=%v", before, after)
	}
}

// A pure re-probe with no on-disk change must return an Equal time, so the
// caller can short-circuit the full scan.
func TestScratchModTimeStableWhenUnchanged(t *testing.T) {
	repo := t.TempDir()
	writeIssue(t, repo, "f", "01-a.md", "# A\n\nStatus: ready-for-agent\n")
	a, err := ScratchModTime(repo)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ScratchModTime(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !a.Equal(b) {
		t.Fatalf("probe not stable across calls: %v vs %v", a, b)
	}
}

// A torn read (the file mid-rewrite) must surface as an error, not as status "".
func TestReadStatusErrorsWithoutStatusLine(t *testing.T) {
	repo := t.TempDir()
	empty := writeIssue(t, repo, "f", "01-a.md", "")
	if st, err := ReadStatus(empty); err == nil {
		t.Errorf("ReadStatus(empty file) = %q, nil; want error", st)
	}
	partial := writeIssue(t, repo, "f", "02-b.md", "# B\n")
	if st, err := ReadStatus(partial); err == nil {
		t.Errorf("ReadStatus(no Status line) = %q, nil; want error", st)
	}
	ok := writeIssue(t, repo, "f", "03-c.md", "# C\n\nStatus: Asking\n")
	if st, err := ReadStatus(ok); err != nil || st != "asking" {
		t.Errorf("ReadStatus = %q, %v; want asking", st, err)
	}
}

// Writers replace the file via rename, leaving no temp files behind.
func TestWritersLeaveNoTempFiles(t *testing.T) {
	repo := t.TempDir()
	path := writeIssue(t, repo, "f", "01-a.md", "# A\n\nStatus: claimed\n\n## What to build\n\nx\n")
	if err := SetStatus(path, "asking"); err != nil {
		t.Fatal(err)
	}
	if err := appendComment(path, "a comment"); err != nil {
		t.Fatal(err)
	}
	if err := appendSection(path, "Answer", "done"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("issues dir holds %d entries, want only the issue file", len(entries))
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"Status: asking", "a comment", "## Answer"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("file missing %q:\n%s", want, data)
		}
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", fi.Mode().Perm())
	}
}
