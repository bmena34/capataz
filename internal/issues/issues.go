// Package issues reads the local-markdown issue backlog for a repo — the
// .scratch/<feature>/issues/NN-slug.md files created by the mattpocock-style
// planning skills (/to-issues, /triage, /wayfinder). The files are the source
// of truth: capataz parses them for the dashboard and rewrites the Status line
// when an agent claims or resolves an issue.
//
// The format is deliberately tolerant. An issue file has a title (the first
// "# " heading, else a slug derived from the filename) and a small set of
// header lines near the top:
//
//	Status: ready-for-agent        # triage state, or claimed/resolved (wayfinder)
//	Type: task                     # research | prototype | grilling | task
//	Blocked by: 01, 03             # issue numbers in the same feature
//	Touches: internal/tui/, foo.go # repo-relative files/dirs the issue edits
//
// Unknown lines are ignored, and a missing .scratch/ directory is not an error.
package issues

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// scratchDir is the per-repo directory the planning skills write issues into.
const scratchDir = ".scratch"

// Issue is one .scratch/<feature>/issues/NN-slug.md file.
type Issue struct {
	Feature   string   // feature slug (the parent directory under .scratch)
	Num       string   // the NN filename prefix, e.g. "01" ("" if unnumbered)
	Slug      string   // the slug portion of the filename
	Title     string   // first "# " heading, else a readable form of the slug
	Status    string   // Status: line, lowercased ("" if absent)
	Type      string   // Type: line, lowercased ("" if absent)
	BlockedBy []string // Blocked by: issue numbers in the same feature
	Blocked   bool     // true if any blocker is not yet resolved
	Touches   []string // Touches: repo-relative files/dirs the issue edits (nil = unknown scope)
	Path      string   // absolute path to the markdown file
}

// Feature is one .scratch/<slug>/ directory and the issues under it.
type Feature struct {
	Slug   string  // directory name under .scratch
	Dir    string  // absolute path to the feature directory
	Issues []Issue // sorted by Num, then filename
}

// Done reports whether a status counts as finished for blocking purposes.
// Covers both the triage vocabulary (wontfix) and wayfinder (resolved), plus
// the everyday "done"/"closed".
func Done(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "resolved", "done", "closed", "wontfix":
		return true
	}
	return false
}

// Bucket collapses an issue's granular status into the four categories the
// dashboard tracker shows: "done" (terminal), "blocked" (waiting on another
// issue), "doing" (an agent has claimed it), or "todo" (everything else that's
// open). This is the single mapping from the open-ended Status vocabulary to
// the tracker's fixed set.
func Bucket(iss Issue) string {
	switch {
	case Done(iss.Status):
		return "done"
	case iss.Blocked:
		return "blocked"
	case iss.Status == "claimed":
		return "doing"
	default:
		return "todo"
	}
}

var (
	fileRe   = regexp.MustCompile(`^(\d+)-(.+)\.md$`)
	headerRe = regexp.MustCompile(`(?i)^(status|type|blocked by|touches)\s*:\s*(.*)$`)
	titleRe  = regexp.MustCompile(`^#\s+(.+)$`)
)

// Scan reads every feature backlog under repoRoot/.scratch. It returns features
// sorted by slug, each with its issues sorted by number. A repo with no
// .scratch/ directory yields (nil, nil) — the absence of a backlog is not an
// error.
func Scan(repoRoot string) ([]Feature, error) {
	if repoRoot == "" {
		return nil, nil
	}
	root := filepath.Join(repoRoot, scratchDir)
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var features []Feature
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		f := scanFeature(filepath.Join(root, e.Name()), e.Name())
		if len(f.Issues) > 0 {
			features = append(features, f)
		}
	}
	sort.Slice(features, func(i, j int) bool { return features[i].Slug < features[j].Slug })
	return features, nil
}

// ScratchModTime returns the newest modification time across the .scratch tree
// for a repo, without reading or parsing any issue file — a cheap change probe
// callers can compare across ticks to decide whether a full Scan is warranted.
// It stats the .scratch root, every feature directory, every issues/ directory,
// and every issue .md file. The .md files matter because rewriting an existing
// file's Status (SetStatus) bumps the file's mtime but not the parent dir's, so
// a dir-only probe would miss claim/resolve edits.
//
// A missing .scratch/ directory yields (zero time, nil) — the absence of a
// backlog is not an error, and a zero time compares distinctly against any real
// mtime so it still triggers a scan if a backlog later appears. The returned
// time carries no monotonic clock reading (it comes from the filesystem), so
// callers compare it with Time.Equal.
func ScratchModTime(repoRoot string) (time.Time, error) {
	if repoRoot == "" {
		return time.Time{}, nil
	}
	root := filepath.Join(repoRoot, scratchDir)
	rootInfo, err := os.Stat(root)
	if os.IsNotExist(err) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}

	newest := rootInfo.ModTime()
	bump := func(t time.Time) {
		if t.After(newest) {
			newest = t
		}
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return time.Time{}, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		featDir := filepath.Join(root, e.Name())
		if info, ierr := e.Info(); ierr == nil {
			bump(info.ModTime())
		}
		issuesDir := filepath.Join(featDir, "issues")
		issueEntries, ierr := os.ReadDir(issuesDir)
		if ierr != nil {
			continue // no issues/ dir for this feature: nothing more to stat
		}
		if info, serr := os.Stat(issuesDir); serr == nil {
			bump(info.ModTime())
		}
		for _, ie := range issueEntries {
			if ie.IsDir() || !strings.HasSuffix(ie.Name(), ".md") {
				continue
			}
			if info, serr := ie.Info(); serr == nil {
				bump(info.ModTime())
			}
		}
	}
	return newest, nil
}

// scanFeature reads the issues/ directory of a single feature.
func scanFeature(dir, slug string) Feature {
	f := Feature{Slug: slug, Dir: dir}
	entries, err := os.ReadDir(filepath.Join(dir, "issues"))
	if err != nil {
		return f
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		iss := parseIssue(filepath.Join(dir, "issues", e.Name()), slug)
		f.Issues = append(f.Issues, iss)
	}
	sort.Slice(f.Issues, func(i, j int) bool {
		if f.Issues[i].Num != f.Issues[j].Num {
			return f.Issues[i].Num < f.Issues[j].Num
		}
		return f.Issues[i].Path < f.Issues[j].Path
	})
	markBlocked(f.Issues)
	return f
}

// markBlocked sets Blocked on each issue whose Blocked-by list names a sibling
// that is not yet Done. Unknown numbers are treated as still-blocking so a
// dangling reference errs toward "not ready". Two edge cases are handled
// deliberately: an issue never blocks itself (a self-referencing number is
// skipped), and when several files share a number a blocker counts as satisfied
// only when *every* file with that number is Done — so a duplicate can't
// silently resolve a block against the wrong file.
func markBlocked(list []Issue) {
	byNum := make(map[string][]string, len(list)) // number -> each file's status
	for _, iss := range list {
		if iss.Num != "" {
			byNum[iss.Num] = append(byNum[iss.Num], iss.Status)
		}
	}
	// numDone reports whether a blocker number is satisfied: it must name at
	// least one issue, and every issue sharing that number must be Done.
	numDone := func(num string) bool {
		statuses, ok := byNum[num]
		if !ok || len(statuses) == 0 {
			return false // unknown/dangling number: still blocking
		}
		for _, s := range statuses {
			if !Done(s) {
				return false
			}
		}
		return true
	}
	for i := range list {
		for _, b := range list[i].BlockedBy {
			if b == list[i].Num {
				continue // an issue never blocks itself
			}
			if !numDone(b) {
				list[i].Blocked = true
				break
			}
		}
	}
}

// parseIssue reads and parses one issue file. Parse failures degrade to a
// best-effort Issue (at minimum a title from the filename) rather than an error
// — the dashboard should still show a malformed file rather than hide it.
func parseIssue(path, feature string) Issue {
	iss := Issue{Feature: feature, Path: path}
	base := filepath.Base(path)
	if m := fileRe.FindStringSubmatch(base); m != nil {
		iss.Num, iss.Slug = m[1], m[2]
	} else {
		iss.Slug = strings.TrimSuffix(base, ".md")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		iss.Title = slugToTitle(iss.Slug)
		return iss
	}

	// Header lines live above the first "## " section; stop collecting there so
	// a "Status:" written inside a comment thread can't be mistaken for a header.
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break
		}
		if iss.Title == "" {
			if m := titleRe.FindStringSubmatch(trimmed); m != nil {
				iss.Title = strings.TrimSpace(m[1])
				continue
			}
		}
		if m := headerRe.FindStringSubmatch(trimmed); m != nil {
			key := strings.ToLower(m[1])
			val := strings.TrimSpace(m[2])
			switch key {
			case "status":
				iss.Status = strings.ToLower(val)
			case "type":
				iss.Type = strings.ToLower(val)
			case "blocked by":
				iss.BlockedBy = parseNums(val)
			case "touches":
				iss.Touches = parsePaths(val)
			}
		}
	}
	if iss.Title == "" {
		iss.Title = slugToTitle(iss.Slug)
	}
	return iss
}

// parseNums splits a "Blocked by" value into issue numbers, tolerating "#"
// prefixes, "and", and either commas or spaces as separators. "None" (the
// template's no-blockers placeholder) yields nothing.
func parseNums(s string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '#'
	}) {
		tok = strings.TrimSpace(tok)
		if tok == "" || strings.EqualFold(tok, "and") || strings.EqualFold(tok, "none") {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// parsePaths splits a "Touches" value into repo-relative paths, comma-separated.
// Each path is trimmed and cleaned (trailing slashes dropped so directory and
// file forms compare consistently); empties and the "None" placeholder are
// skipped. A line with no usable paths yields nil — treated as unknown scope.
func parsePaths(s string) []string {
	var out []string
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" || strings.EqualFold(tok, "none") {
			continue
		}
		// Normalize: drop a leading "./" and any trailing slash so a directory
		// like "internal/tui/" compares as the segment path "internal/tui".
		tok = strings.TrimPrefix(tok, "./")
		tok = strings.TrimRight(tok, "/")
		if tok == "" {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// PathsOverlap reports whether two repo-relative paths refer to overlapping
// scope: they overlap when they are equal, or when one is a path-segment prefix
// of the other. Prefix is by whole segments — "internal/tui" overlaps
// "internal/tui/issues.go" but "internal/t" does NOT overlap "internal/tui".
func PathsOverlap(a, b string) bool {
	a = strings.TrimRight(strings.TrimPrefix(a, "./"), "/")
	b = strings.TrimRight(strings.TrimPrefix(b, "./"), "/")
	if a == b {
		return true
	}
	// Ensure a is the shorter (candidate prefix); if b is a prefix of a, the
	// prefix must end on a segment boundary in a.
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a+"/")
}

// touchesOverlap reports whether any path in one issue's Touches overlaps any
// path in the other. An issue with no Touches has unknown scope and therefore
// conservatively overlaps everything (never parallel-safe).
func touchesOverlap(a, b Issue) bool {
	if len(a.Touches) == 0 || len(b.Touches) == 0 {
		return true
	}
	for _, pa := range a.Touches {
		for _, pb := range b.Touches {
			if PathsOverlap(pa, pb) {
				return true
			}
		}
	}
	return false
}

// blockingEdge reports whether either issue lists the other as a Blocked-by
// dependency (by issue number), so waves never pair a dependency with its
// dependent even if their file scopes happen to be disjoint.
func blockingEdge(a, b Issue) bool {
	if a.Num != "" {
		for _, n := range b.BlockedBy {
			if n == a.Num {
				return true
			}
		}
	}
	if b.Num != "" {
		for _, n := range a.BlockedBy {
			if n == b.Num {
				return true
			}
		}
	}
	return false
}

// ParallelSafe groups the currently-ready issues (Bucket == "todo") into waves
// that are safe to run in parallel: within a wave every pair of issues has
// pairwise-disjoint Touches AND no Blocked-by edge between them. It is advisory
// and dynamic — computed from Touches + Blocked-by, not a stored per-issue flag,
// since safety is pairwise and shifts as blockers resolve.
//
// The grouping is greedy over the list order (which Scan sorts by feature then
// number): each ready issue joins the first existing wave it is compatible with,
// else it starts a new wave. Issues with unknown scope (no Touches) conflict
// with everything and land in singleton waves. Callers typically care only about
// waves with more than one member.
func ParallelSafe(list []Issue) [][]Issue {
	var ready []Issue
	for _, iss := range list {
		if Bucket(iss) == "todo" {
			ready = append(ready, iss)
		}
	}
	var waves [][]Issue
	for _, iss := range ready {
		placed := false
		for wi := range waves {
			ok := true
			for _, member := range waves[wi] {
				if touchesOverlap(iss, member) || blockingEdge(iss, member) {
					ok = false
					break
				}
			}
			if ok {
				waves[wi] = append(waves[wi], iss)
				placed = true
				break
			}
		}
		if !placed {
			waves = append(waves, []Issue{iss})
		}
	}
	return waves
}

// MutuallyParallelSafe reports whether a hand-picked set of issues is safe to
// dispatch in parallel: every pair must have disjoint Touches AND no Blocked-by
// edge between them, and no issue may have unknown scope (empty Touches, which
// conservatively conflicts with everything). Unlike ParallelSafe it does not
// filter by bucket — the set is exactly what the operator selected — it only
// judges pairwise safety. A set of fewer than two issues is trivially safe.
func MutuallyParallelSafe(set []Issue) bool {
	for i := range set {
		if len(set[i].Touches) == 0 {
			return false // unknown scope: never safe
		}
		for j := i + 1; j < len(set); j++ {
			if touchesOverlap(set[i], set[j]) || blockingEdge(set[i], set[j]) {
				return false
			}
		}
	}
	return true
}

// BranchPrefix is the prefix capataz puts on every dispatched worktree branch:
// dispatch cuts `capataz/<feature>-<NN>-<slug>`, so Round 2's merge-back scans
// only branches carrying it.
const BranchPrefix = "capataz/"

// MatchBranch finds the issue in list that a dispatched worktree branch belongs
// to. A dispatch branch is `capataz/<feature>-<NN>-<slug>`; the reliable join
// key is Feature + Num, so MatchBranch pairs each issue with its expected branch
// (via the same construction dispatch uses) and returns the first match. It
// returns (issue, true) on a hit, (zero, false) otherwise — including for any
// branch without the capataz/ prefix.
func MatchBranch(branch string, list []Issue) (Issue, bool) {
	if !strings.HasPrefix(branch, BranchPrefix) {
		return Issue{}, false
	}
	for _, iss := range list {
		if DispatchBranch(iss) == branch {
			return iss, true
		}
	}
	return Issue{}, false
}

// DispatchBranch returns the worktree branch name for an issue —
// `capataz/<feature>-<NN>-<slug>`, using "--" for an unnumbered issue. It is the
// single source of truth for the dispatch branch shape so dispatch (Round 1) and
// merge-back matching (Round 2) can't drift apart.
func DispatchBranch(iss Issue) string {
	num := iss.Num
	if num == "" {
		num = "--"
	}
	return BranchPrefix + iss.Feature + "-" + num + "-" + iss.Slug
}

// slugToTitle turns "add-login-form" into "Add login form".
func slugToTitle(slug string) string {
	if slug == "" {
		return "(untitled)"
	}
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	if len(words) > 0 && words[0] != "" {
		r, size := utf8.DecodeRuneInString(words[0])
		words[0] = string(unicode.ToUpper(r)) + words[0][size:]
	}
	return strings.Join(words, " ")
}

// SetStatus rewrites the Status line of an issue file in place, preserving the
// rest of the file. If there is no Status line it inserts one just after the
// title heading (or at the top when there is no heading). status is written
// verbatim.
func SetStatus(path, status string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	newLine := "Status: " + status

	insertAt := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			break // don't touch anything below the first section
		}
		if headerRe.MatchString(trimmed) {
			if m := headerRe.FindStringSubmatch(trimmed); m != nil && strings.EqualFold(m[1], "status") {
				lines[i] = newLine
				return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
			}
		}
		if titleRe.MatchString(trimmed) {
			insertAt = i + 1
		}
	}

	// No existing Status line: insert after the title (or at the top). When
	// inserting right after a title, add a blank line for readability.
	var out []string
	out = append(out, lines[:insertAt]...)
	if insertAt > 0 && insertAt <= len(lines) && strings.TrimSpace(lines[insertAt-1]) != "" && titleRe.MatchString(strings.TrimSpace(lines[insertAt-1])) {
		out = append(out, "", newLine)
	} else {
		out = append(out, newLine)
	}
	out = append(out, lines[insertAt:]...)
	return os.WriteFile(path, []byte(strings.Join(out, "\n")), 0o644)
}
