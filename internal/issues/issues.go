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
//
// Unknown lines are ignored, and a missing .scratch/ directory is not an error.
package issues

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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
	headerRe = regexp.MustCompile(`(?i)^(status|type|blocked by)\s*:\s*(.*)$`)
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
// dangling reference errs toward "not ready".
func markBlocked(list []Issue) {
	status := make(map[string]string, len(list))
	for _, iss := range list {
		if iss.Num != "" {
			status[iss.Num] = iss.Status
		}
	}
	for i := range list {
		for _, b := range list[i].BlockedBy {
			s, ok := status[b]
			if !ok || !Done(s) {
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

// slugToTitle turns "add-login-form" into "Add login form".
func slugToTitle(slug string) string {
	if slug == "" {
		return "(untitled)"
	}
	words := strings.Split(strings.ReplaceAll(slug, "_", "-"), "-")
	if len(words) > 0 && words[0] != "" {
		words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
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
