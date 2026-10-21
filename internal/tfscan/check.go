package tfscan

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// Runner runs an external command (git, gh) in dir and returns its stdout.
// Tests substitute a fake keyed on the command line.
type Runner interface {
	Run(dir, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
		}
		return string(out), fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Finding kinds.
const (
	KindUnknown    = "unknown-scope"
	KindPR         = "open-pr"
	KindPRsSkipped = "prs-not-checked"
	KindWorktree   = "worktree"
	KindStale      = "stale-plan"
)

// Finding is one race-condition signal. Root is empty when not root-specific.
type Finding struct {
	Kind    string `json:"kind"`
	Root    string `json:"root,omitempty"`
	PR      int    `json:"pr,omitempty"`
	URL     string `json:"url,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

// Report is the result of a check; its JSON form is the --json output.
type Report struct {
	Base     string    `json:"base"`
	Changed  []string  `json:"changed"`
	Roots    []string  `json:"roots"`
	Notes    []string  `json:"notes"`
	Findings []Finding `json:"findings"`
}

// prFileLimit is the per-PR file count gh pr list returns at most; a list this
// long may be truncated, so the full list is fetched with gh pr diff.
const prFileLimit = 100

// Check runs the race checks for the checkout at dir against base ("" picks
// origin/main, then main). Changed files include uncommitted and untracked
// work, since capataz-work runs it before committing.
func Check(r Runner, dir, base string) (*Report, error) {
	top, err := r.Run(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, err
	}
	top = strings.TrimSpace(top)
	if base, err = resolveBase(r, top, base); err != nil {
		return nil, err
	}
	mb, err := mergeBase(r, top, base, "HEAD")
	if err != nil {
		return nil, err
	}
	changed, err := workingChanges(r, top, mb)
	if err != nil {
		return nil, err
	}
	rep := &Report{Base: base, Changed: tfFiles(changed), Roots: []string{}, Notes: []string{}, Findings: []Finding{}}
	if len(rep.Changed) == 0 {
		return rep, nil
	}
	m, err := Scan(top)
	if err != nil {
		return nil, err
	}
	rep.Notes = append(rep.Notes, m.Notes...)
	roots, unknown := m.RootsFor(rep.Changed)
	if roots != nil {
		rep.Roots = roots
	}
	if unknown {
		rep.Findings = append(rep.Findings, Finding{Kind: KindUnknown,
			Message: "some changed Terraform files map to no known root; scope unknown"})
	}
	branch, _ := r.Run(top, "git", "rev-parse", "--abbrev-ref", "HEAD")
	branch = strings.TrimSpace(branch)

	rep.Findings = append(rep.Findings, checkPRs(r, top, branch, m, roots)...)
	wt, err := checkWorktrees(r, top, base, m, roots)
	if err != nil {
		return nil, err
	}
	rep.Findings = append(rep.Findings, wt...)

	baseChanged, err := r.Run(top, "git", "diff", "--name-only", mb+".."+base)
	if err != nil {
		return nil, err
	}
	for _, root := range overlap(m, roots, lines(baseChanged)) {
		rep.Findings = append(rep.Findings, Finding{Kind: KindStale, Root: root,
			Message: fmt.Sprintf("%s changed %s since this branch was cut; re-plan against it", base, root)})
	}
	return rep, nil
}

func resolveBase(r Runner, dir, base string) (string, error) {
	cands := []string{base}
	if base == "" {
		cands = []string{"origin/main", "main"}
	}
	for _, c := range cands {
		if _, err := r.Run(dir, "git", "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("base ref not found: %s", strings.Join(cands, ", "))
}

func mergeBase(r Runner, dir, a, b string) (string, error) {
	out, err := r.Run(dir, "git", "merge-base", a, b)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// workingChanges lists files in dir's working tree that differ from rev,
// tracked or untracked.
func workingChanges(r Runner, dir, rev string) ([]string, error) {
	diff, err := r.Run(dir, "git", "diff", "--name-only", rev)
	if err != nil {
		return nil, err
	}
	untracked, err := r.Run(dir, "git", "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return append(lines(diff), lines(untracked)...), nil
}

func checkPRs(r Runner, dir, branch string, m *Map, roots []string) []Finding {
	skipped := func(err error) []Finding {
		msg := "open PRs not checked"
		if errors.Is(err, exec.ErrNotFound) {
			msg += ": gh not installed"
		} else {
			msg += ": " + err.Error()
		}
		return []Finding{{Kind: KindPRsSkipped, Message: msg}}
	}
	out, err := r.Run(dir, "gh", "pr", "list", "--state", "open", "--limit", "200", "--json", "number,url,headRefName,files")
	if err != nil {
		return skipped(err)
	}
	var prs []struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
		Files       []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return skipped(fmt.Errorf("parse gh pr list: %w", err))
	}
	var fs []Finding
	for _, pr := range prs {
		if pr.HeadRefName == branch {
			continue
		}
		var files []string
		for _, f := range pr.Files {
			files = append(files, f.Path)
		}
		if len(files) >= prFileLimit {
			if d, err := r.Run(dir, "gh", "pr", "diff", fmt.Sprint(pr.Number), "--name-only"); err == nil {
				files = lines(d)
			}
		}
		for _, root := range overlap(m, roots, files) {
			fs = append(fs, Finding{Kind: KindPR, Root: root, PR: pr.Number, URL: pr.URL, Branch: pr.HeadRefName,
				Message: fmt.Sprintf("PR #%d (%s) also changes %s: %s", pr.Number, pr.HeadRefName, root, pr.URL)})
		}
	}
	return fs
}

func checkWorktrees(r Runner, top, base string, m *Map, roots []string) ([]Finding, error) {
	out, err := r.Run(top, "git", "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var fs []Finding
	for _, wt := range parseWorktrees(out) {
		if path.Clean(wt.path) == path.Clean(top) || wt.bare || wt.head == "" {
			continue
		}
		mb, err := mergeBase(r, wt.path, base, wt.head)
		if err != nil {
			continue // unrelated history or a pruned worktree: nothing to compare
		}
		files, err := workingChanges(r, wt.path, mb)
		if err != nil {
			continue
		}
		label := wt.branch
		if label == "" {
			label = "detached " + wt.head
		}
		for _, root := range overlap(m, roots, files) {
			fs = append(fs, Finding{Kind: KindWorktree, Root: root, Branch: wt.branch, Path: wt.path,
				Message: fmt.Sprintf("worktree %s (%s) also changes %s", label, wt.path, root)})
		}
	}
	return fs, nil
}

type worktree struct {
	path, head, branch string
	bare               bool
}

func parseWorktrees(out string) []worktree {
	var wts []worktree
	for block := range strings.SplitSeq(strings.TrimSpace(out), "\n\n") {
		var wt worktree
		for l := range strings.SplitSeq(block, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
			switch k {
			case "worktree":
				wt.path = v
			case "HEAD":
				wt.head = v
			case "branch":
				wt.branch = strings.TrimPrefix(v, "refs/heads/")
			case "bare":
				wt.bare = true
			}
		}
		if wt.path != "" {
			wts = append(wts, wt)
		}
	}
	return wts
}

// overlap returns the roots in mine that other's changes affect. Changes of
// unknown scope overlap every root, reported as "unknown".
func overlap(m *Map, mine, other []string) []string {
	theirs, unknown := m.RootsFor(other)
	if unknown && len(mine) > 0 {
		return []string{"unknown"}
	}
	set := map[string]bool{}
	for _, r := range theirs {
		set[r] = true
	}
	var out []string
	for _, r := range mine {
		if set[r] {
			out = append(out, r)
		}
	}
	return out
}

func tfFiles(files []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, f := range files {
		b := path.Base(f)
		if (strings.HasSuffix(b, ".tf") || strings.HasSuffix(b, ".tfvars") || b == ".terraform.lock.hcl") && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

func lines(s string) []string {
	var out []string
	for l := range strings.SplitSeq(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// Run implements `capataz tf <subcommand>` and returns the exit code:
// 0 clean, 1 findings, 2 error.
func Run(args []string) int {
	return run(execRunner{}, ".", args, os.Stdout, os.Stderr)
}

func run(r Runner, dir string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "usage: capataz tf check [--base <ref>] [--json]")
		return 2
	}
	fs := flag.NewFlagSet("tf check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "base ref (default origin/main, else main)")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	rep, err := Check(r, dir, *base)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.Encode(rep)
	} else {
		printReport(stdout, rep)
	}
	if len(rep.Findings) > 0 {
		return 1
	}
	return 0
}

func printReport(w io.Writer, rep *Report) {
	if len(rep.Changed) == 0 {
		fmt.Fprintf(w, "no Terraform changes vs %s\n", rep.Base)
		return
	}
	fmt.Fprintf(w, "Terraform changes vs %s: %d file(s), roots: %s\n", rep.Base, len(rep.Changed), strings.Join(rep.Roots, ", "))
	for _, n := range rep.Notes {
		fmt.Fprintln(w, "note:", n)
	}
	if len(rep.Findings) == 0 {
		fmt.Fprintln(w, "clean: no race-condition findings")
		return
	}
	fmt.Fprintf(w, "%d finding(s):\n", len(rep.Findings))
	for _, f := range rep.Findings {
		fmt.Fprintf(w, "  [%s] %s\n", f.Kind, f.Message)
	}
}
