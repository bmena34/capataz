package issues

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bmena34/capataz/internal/worktree"
)

// Run is the `capataz issue …` subcommand: the small CLI the capataz-work skill
// calls to claim and resolve issues so their Status stays consistent no matter
// which agent (or human) touches the file. The markdown file is the source of
// truth; these commands just edit it.
//
//	capataz issue claim   <path>              set Status: claimed
//	capataz issue resolve <path> [--answer …] set Status: resolved (+ optional answer)
//	capataz issue status  <path> <status>     set Status: <status>
//	capataz issue comment <path> <text>       append a note under ## Comments
//	capataz issue list    <repo>              print the backlog for a repo
//	capataz issue new     <feature> <title> [--body … --type … --repo …]
func Run(args []string) error {
	if len(args) == 0 {
		return usageErr()
	}
	switch args[0] {
	case "new":
		if len(args) < 3 {
			return usageErr()
		}
		return create(args[1], args[2], args[3:])
	case "claim":
		if len(args) < 2 {
			return usageErr()
		}
		force := hasFlag(args[2:], "--force")
		return claimCLI(args[1], force)
	case "resolve":
		if len(args) < 2 {
			return usageErr()
		}
		answer := flagValue(args[2:], "--answer")
		if err := setStatusCLI(args[1], StatusResolved); err != nil {
			return err
		}
		if answer != "" {
			if err := appendSection(args[1], "Answer", answer); err != nil {
				return err
			}
		}
		return nil
	case "status":
		if len(args) < 3 {
			return usageErr()
		}
		return setStatusCLI(args[1], args[2])
	case "comment":
		if len(args) < 3 {
			return usageErr()
		}
		return commentCLI(args[1], strings.Join(args[2:], " "))
	case "list":
		repo := "."
		if len(args) > 1 {
			repo = args[1]
		}
		return list(repo)
	case "waves":
		if len(args) < 2 {
			return usageErr()
		}
		if !hasFlag(args[2:], "--json") {
			return fmt.Errorf("waves: --json flag is required")
		}
		return wavesJSON(args[1])
	default:
		return usageErr()
	}
}

func setStatusCLI(path, status string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("issue file not found: %s", path)
	}
	if err := SetStatus(path, status); err != nil {
		return err
	}
	fmt.Printf("%s → Status: %s\n", path, status)
	return nil
}

// claimCLI is the M1→M2 claim boundary: an atomic compare-and-set that moves an
// issue from ready-for-agent to claimed. Unlike setStatusCLI it is NOT
// unconditional — only a ready-for-agent issue is claimable, so two agents can't
// both claim the same file and silently clobber each other. --force bypasses the
// state check (operator override / re-claim) but still takes the lock.
//
// Atomicity: the read-current-status → check → write sequence runs inside a
// per-issue-file O_EXCL lock (lockClaim, keyed on the issue file, not the feature
// dir), and the status is re-read from disk INSIDE the lock so two near-
// simultaneous claims can't both pass the check. resolve/status stay
// unconditional — this strictness is scoped to claim.
func claimCLI(path string, force bool) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("issue file not found: %s", path)
	}

	unlock := lockClaim(path)
	defer unlock()

	if !force {
		// Re-read the status from disk inside the lock so the check reflects any
		// claim that landed while we were waiting for the lock.
		status := normStatus(readStatus(path))
		switch status {
		case StatusReadyForAgent:
			// claimable — fall through
		case StatusClaimed:
			return fmt.Errorf("%s already claimed (use --force to re-claim)", path)
		default:
			return fmt.Errorf("%s not claimable: %s (use --force to override)", path, status)
		}
	}

	if err := SetStatus(path, StatusClaimed); err != nil {
		return err
	}
	fmt.Printf("%s → Status: %s\n", path, StatusClaimed)
	return nil
}

// readStatus reads the current Status of an issue file by reusing parseIssue
// (the same tolerant header parser Scan uses), so claim sees exactly the status
// the viewer does. Returns "" when the file is unreadable or has no Status
// line — normStatus("") never matches a claimable state, so a malformed file is
// treated as not-claimable rather than silently claimed.
func readStatus(path string) string {
	return parseIssue(path, "").Status
}

// create writes a new ready-for-agent issue at .scratch/<feature>/issues/NN-slug.md,
// numbered one past the highest existing issue in that feature. Used by the
// capataz-review skill to file review findings back into the backlog.
func create(feature, title string, flags []string) error {
	repo := flagValue(flags, "--repo")
	if repo == "" {
		repo = "."
	}
	// The backlog lives in the primary checkout, so a `new` run from inside a
	// linked worktree still writes to the shared backlog. A plain repo is its
	// own primary (behavior-preserving); on any git error keep repo as-is.
	if p, err := worktree.PrimaryCheckout(repo); err == nil {
		repo = p
	}
	typ := flagValue(flags, "--type")
	if typ == "" {
		typ = "task"
	}
	body := flagValue(flags, "--body")
	if body == "" {
		body = "<describe the end-to-end change>"
	}

	dir := filepath.Join(repo, scratchDir, feature, "issues")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	// Serialize number allocation so two concurrent `issue new` calls (e.g. a
	// review session filing findings while capataz-splitter runs) can't grab the
	// same number and produce duplicate-numbered files.
	unlock := lockFeature(dir)
	defer unlock()

	num := nextNum(dir)
	slug := slugify(title)
	path := filepath.Join(dir, fmt.Sprintf("%02d-%s.md", num, slug))
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("issue already exists: %s", path)
	}
	content := fmt.Sprintf(`# %s

Status: %s
Type: %s
Blocked by: None

## What to build

%s

## Acceptance criteria

- [ ]
`, title, StatusReadyForAgent, typ, body)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}

// lockFeature serializes `issue new` within one feature's issues/ directory via
// an O_EXCL lock file, returning a release func. Best-effort and deadlock-proof:
// it steals a lock older than a few seconds (a crashed creator) and, if it still
// can't acquire, proceeds unlocked rather than blocking forever. The lock file
// is not a .md and is ignored by Scan/nextNum.
func lockFeature(dir string) func() {
	lock := filepath.Join(dir, ".capataz-new.lock")
	for i := 0; i < 100; i++ {
		if f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			return func() { os.Remove(lock) }
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > 5*time.Second {
			os.Remove(lock) // steal a stale lock left by a crashed creator
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {} // gave up waiting; proceed unlocked (degrade, don't hang)
}

// lockClaim serializes concurrent `issue claim` calls against a single issue
// file. Same O_EXCL best-effort scheme as lockFeature, but keyed on the issue
// FILE (<path>.capataz-claim.lock) rather than the feature dir, so it gates the
// per-file compare-and-set without blocking claims of unrelated issues. The lock
// file is not a .md and is ignored by Scan/parseIssue.
func lockClaim(path string) func() {
	lock := path + ".capataz-claim.lock"
	for i := 0; i < 100; i++ {
		if f, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); err == nil {
			f.Close()
			return func() { os.Remove(lock) }
		}
		if fi, err := os.Stat(lock); err == nil && time.Since(fi.ModTime()) > 5*time.Second {
			os.Remove(lock) // steal a stale lock left by a crashed claimer
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}
	return func() {} // gave up waiting; proceed unlocked (degrade, don't hang)
}

// nextNum returns one past the highest NN prefix among the issue files in dir
// (1 when the directory is empty or absent).
func nextNum(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 1
	}
	max := 0
	for _, e := range entries {
		if m := fileRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

// slugify turns a title into a filename-safe kebab slug.
func slugify(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 50 {
		slug = strings.Trim(slug[:50], "-")
	}
	if slug == "" {
		slug = "issue"
	}
	return slug
}

// commentCLI appends a note under the issue file's "## Comments" section
// (creating it if absent). Used by the review and fix-conflict skills to record
// findings back onto an issue.
func commentCLI(path, text string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("issue file not found: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	s := strings.TrimRight(string(data), "\n")
	// Append under an existing Comments section (assumed last), else start one.
	if strings.HasPrefix(s, "## Comments") || strings.Contains(s, "\n## Comments") {
		s += "\n\n" + strings.TrimSpace(text) + "\n"
	} else {
		s += "\n\n## Comments\n\n" + strings.TrimSpace(text) + "\n"
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		return err
	}
	fmt.Printf("%s → comment added\n", path)
	return nil
}

// appendSection appends "## <heading>\n\n<body>" to a file, adding a leading
// blank line so it doesn't run into whatever came before.
func appendSection(path, heading, body string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := strings.TrimRight(string(data), "\n")
	text += fmt.Sprintf("\n\n## %s\n\n%s\n", heading, body)
	return os.WriteFile(path, []byte(text), 0o644)
}

// list prints the backlog for a repo, grouped by feature — a plain-text view
// for scripts and debugging (`capataz issues` is the interactive view).
func list(repo string) error {
	feats, err := Scan(repo)
	if err != nil {
		return err
	}
	if len(feats) == 0 {
		fmt.Println("no issues found under", repo+"/.scratch")
		return nil
	}
	for _, f := range feats {
		fmt.Printf("%s/\n", f.Slug)
		for _, iss := range f.Issues {
			marks := iss.Status
			if marks == "" {
				marks = "—"
			}
			if iss.Blocked {
				marks += " (blocked)"
			}
			num := iss.Num
			if num == "" {
				num = "--"
			}
			fmt.Printf("  %s  %-16s  %s\n", num, marks, iss.Title)
		}
	}
	return nil
}

// waveIssue is the per-issue entry emitted in each wave by wavesJSON. It
// carries the fields herdr-port's orchestrator needs to dispatch work without
// re-parsing issue files: the issue number, absolute path (the canonical issue
// identity), feature slug, current status, and the dispatch branch name (the
// single source of truth for worktree branch naming, matching DispatchBranch).
type waveIssue struct {
	Number  string `json:"number"`
	Path    string `json:"path"`
	Feature string `json:"feature"`
	Status  string `json:"status"`
	Branch  string `json:"branch"`
}

// wavesOutput is the top-level JSON envelope: an ordered array of waves, each
// a list of waveIssue objects. Wave 0 is the first parallelizable batch;
// subsequent waves depend on earlier ones (their blockers are in earlier waves).
type wavesOutput struct {
	Feature string        `json:"feature"`
	Waves   [][]waveIssue `json:"waves"`
}

// wavesJSON scans the feature's issues, filters to the ones an automated drain
// may dispatch — status ready-for-agent and not blocked — calls ParallelSafe to
// group them into ordered parallel waves, and emits the result as JSON on
// stdout. Non-ready statuses (needs-triage, needs-info, ready-for-human) are
// excluded even though they share the "todo" bucket: `capataz issue claim`
// only succeeds from ready-for-agent, so emitting them would make a drain spin
// on issues it can never claim. The --json flag is required by the caller.
func wavesJSON(feature string) error {
	// Resolve the repo root by walking up from cwd to find the .scratch/ parent.
	// We do it by using the primary checkout of the cwd, falling back to ".".
	repo := "."
	if p, err := worktree.PrimaryCheckout("."); err == nil {
		repo = p
	}

	feats, err := Scan(repo)
	if err != nil {
		return err
	}

	var target *Feature
	for i := range feats {
		if feats[i].Slug == feature {
			target = &feats[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("feature %q not found under %s/.scratch", feature, repo)
	}

	// Filter to issues a drain may actually claim (ready-for-agent, not blocked),
	// then group into parallel-safe waves. Dispatchable guards the not-blocked
	// half; the status check excludes other todo-bucket statuses (needs-triage
	// etc.) that claim would reject.
	var dispatchable []Issue
	for _, iss := range target.Issues {
		if Dispatchable(iss) && iss.Status == StatusReadyForAgent {
			dispatchable = append(dispatchable, iss)
		}
	}
	raw := ParallelSafe(dispatchable)

	out := wavesOutput{Feature: feature}
	for _, wave := range raw {
		var w []waveIssue
		for _, iss := range wave {
			w = append(w, waveIssue{
				Number:  iss.Num,
				Path:    iss.Path,
				Feature: iss.Feature,
				Status:  iss.Status,
				Branch:  DispatchBranch(iss),
			})
		}
		out.Waves = append(out.Waves, w)
	}
	if out.Waves == nil {
		out.Waves = [][]waveIssue{}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// flagValue returns the value following name in args (e.g. --answer "text"), or
// "" if the flag is absent. Also accepts --answer=text.
func flagValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

// hasFlag reports whether a bare flag (e.g. --force) is present in args.
func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func usageErr() error {
	return fmt.Errorf("usage: capataz issue <claim [--force]|resolve|status|comment|list|new|waves> <path|feature> [args]")
}
