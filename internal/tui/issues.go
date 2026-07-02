package tui

import (
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/worktree"
)

// syncSelectedIssues keeps the cached backlog (issueList) pointed at the
// selected session's repo. It reloads from disk when the selection moves to a
// different cwd, or when force is set — the periodic tick forces a refresh to
// pick up claim/resolve edits agents make on disk. Reads are cheap (one
// .scratch tree) and happen here, off the render path, so the inline sidebar
// panel and the modal both render from the cache without scanning.
//
// A forced refresh on the same repo first runs a cheap change probe
// (issues.ScratchModTime — stats the .scratch tree, no file reads or parsing)
// and skips the full scan when the backlog is byte-for-byte unchanged since the
// last load, so idle browsing with the view open doesn't re-parse every issue
// file each tick. An agent's claim/resolve/new edit bumps a stat'd mtime, so it
// still shows up on the next tick. A cwd change always reloads (the probe is
// against the new repo, which has no prior baseline).
//
// The scan root (issueRepo) is the selected cwd resolved to its primary git
// checkout, so a session running in a linked worktree still shows (and starts
// work against) the shared backlog in the primary. That resolution shells out
// to git, so it runs only when the cwd actually changes — never per tick.
func (m *model) syncSelectedIssues(force bool) {
	sel, ok := m.current()
	if !ok {
		m.issueCwd, m.issueRepo, m.issueRepoName, m.issueList = "", "", "", nil
		m.issueMod = time.Time{}
		return
	}
	if sel.Cwd != m.issueCwd {
		m.issueCwd = sel.Cwd
		repo := sel.Cwd
		if p, err := worktree.PrimaryCheckout(sel.Cwd); err == nil {
			repo = p // scope the backlog to the primary checkout
		}
		m.issueRepo = repo
		m.issueRepoName = short(repo)
		m.issueCursor = 0
		m.issueSelected = nil // the previous repo's issues are no longer in view
	} else if !force {
		return // same cwd and no forced refresh: keep the cache
	} else if !m.issueMod.IsZero() {
		// Same cwd, forced refresh: probe the .scratch mtime (cheap — stats only,
		// no file reads) and skip the full scan when nothing changed since the last
		// load. A zero baseline means no backlog was loaded yet (or the repo has
		// none), so we don't short-circuit there — that scan is a single ReadDir.
		if mod, err := issues.ScratchModTime(m.issueRepo); err == nil && mod.Equal(m.issueMod) {
			return
		}
	}
	m.loadIssues()
}

// openIssues opens the interactive issue view for the selected session's repo.
func (m *model) openIssues() {
	m.syncSelectedIssues(true)
	m.issueCursor = 0
	m.mode = modeIssues
}

// loadIssues scans issueRepo into issueList, keeping the cursor on the same
// issue (by path) across reloads. A scan error leaves the list empty.
func (m *model) loadIssues() {
	var selPath string
	if m.issueCursor >= 0 && m.issueCursor < len(m.issueList) {
		selPath = m.issueList[m.issueCursor].Path
	}
	// Record the change-probe baseline BEFORE scanning: if a write lands between
	// the probe and the read, the recorded mtime is the older (pre-write) one, so
	// the next tick's probe sees a newer mtime and reloads. Stat-first errs toward
	// an extra scan, never toward a missed edit.
	if mod, err := issues.ScratchModTime(m.issueRepo); err == nil {
		m.issueMod = mod
	}
	feats, err := issues.Scan(m.issueRepo)
	if err != nil {
		m.status = "issue scan failed: " + err.Error()
		m.issueList = nil
		return
	}
	m.issueList = m.issueList[:0]
	for _, f := range feats {
		m.issueList = append(m.issueList, f.Issues...)
	}
	if selPath != "" {
		for i, iss := range m.issueList {
			if iss.Path == selPath {
				m.issueCursor = i
				break
			}
		}
	}
	m.clampIssueCursor()
	m.refreshReadyMerges()
}

// mergeCandidate is one dispatched worktree branch ready to merge back: its
// linked worktree path, the branch, and the resolved issue it implements.
type mergeCandidate struct {
	Path   string
	Branch string
	Issue  issues.Issue
}

// refreshReadyMerges recomputes the ready-to-merge set for issueRepo from the
// live worktree list joined against the cached backlog. It shells out to git, so
// it runs only from loadIssues (selection change or the forced tick refresh),
// never on the render path. A ready branch is a capataz/-prefixed dispatch
// branch whose backing issue is resolved (Bucket == "done").
func (m *model) refreshReadyMerges() {
	m.readyMerges = nil
	if m.issueRepo == "" {
		return
	}
	wts, err := worktree.ListWT(m.issueRepo)
	if err != nil {
		return // no worktrees / not a repo: nothing to merge, silently
	}
	for _, wt := range wts {
		iss, ok := issues.MatchBranch(wt.Branch, m.issueList)
		if !ok || issues.Bucket(iss) != "done" {
			continue
		}
		m.readyMerges = append(m.readyMerges, mergeCandidate{Path: wt.Path, Branch: wt.Branch, Issue: iss})
	}
}

func (m *model) clampIssueCursor() {
	if m.issueCursor >= len(m.issueList) {
		m.issueCursor = len(m.issueList) - 1
	}
	if m.issueCursor < 0 {
		m.issueCursor = 0
	}
}

func (m model) handleIssuesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if m.gPending {
		m.gPending = false
		if key == "g" {
			m.issueCursor = 0
			m.clampIssueCursor()
			return m, nil
		}
	}
	switch key {
	case "esc", "q":
		m.mode = modeNormal
	case "up", "k":
		m.issueCursor--
		m.clampIssueCursor()
	case "down", "j":
		m.issueCursor++
		m.clampIssueCursor()
	case "g":
		m.gPending = true
	case "G":
		m.issueCursor = len(m.issueList) - 1
		m.clampIssueCursor()
	case " ": // toggle multi-select on the cursor's issue (for parallel dispatch)
		if m.issueCursor >= 0 && m.issueCursor < len(m.issueList) {
			if m.issueSelected == nil {
				m.issueSelected = map[string]bool{}
			}
			path := m.issueList[m.issueCursor].Path
			if m.issueSelected[path] {
				delete(m.issueSelected, path)
			} else {
				m.issueSelected[path] = true
			}
		}
	case "p": // dispatch the selected set in parallel (only if mutually safe)
		return m.dispatchParallel(false)
	case "P": // force parallel dispatch even when the set overlaps
		return m.dispatchParallel(true)
	case "m": // merge back the ready dispatched worktree branches (Round 2)
		return m.mergeReady()
	case "enter", "o":
		if m.issueCursor >= 0 && m.issueCursor < len(m.issueList) {
			m.startIssue = m.issueList[m.issueCursor]
			m.mode = modeStartWork
		}
	case "R": // in-depth review of the selected feature in a fresh session
		if m.issueCursor >= 0 && m.issueCursor < len(m.issueList) {
			return m.startReview(m.issueList[m.issueCursor].Feature)
		}
	}
	return m, nil
}

// startReview spawns a new session in the repo running the capataz-review skill
// over the given feature — a fresh context, so it judges soundness rather than
// trusting the implementers. Its findings return to the backlog as new issues.
func (m model) startReview(feature string) (tea.Model, tea.Cmd) {
	if os.Getenv("TMUX") == "" {
		m.status = "can't spawn: capataz isn't in tmux"
		m.mode = modeNormal
		return m, nil
	}
	prompt := fmt.Sprintf("Use the /capataz-review skill to do an in-depth, fresh-context review of the current changes and file findings as new issues under the %q feature (.scratch/%s).", feature, feature)
	pane, err := newSessionWithSidebar("review-"+feature, m.issueRepo, prompt)
	if err != nil {
		m.status = "review session failed: " + err.Error()
		m.mode = modeNormal
		return m, nil
	}
	m.status = "started fresh review of " + feature
	m.mode = modeNormal
	if err := jump(pane); err != nil {
		m.status = "spawned but couldn't switch: " + err.Error()
		return m, nil
	}
	if m.popup {
		return m, tea.Quit
	}
	return m, nil
}

// selectedIssues returns the currently multi-selected issues, in list order.
func (m model) selectedIssues() []issues.Issue {
	if len(m.issueSelected) == 0 {
		return nil
	}
	var out []issues.Issue
	for _, iss := range m.issueList {
		if m.issueSelected[iss.Path] {
			out = append(out, iss)
		}
	}
	return out
}

// dispatchParallel spawns one isolated-worktree session per selected issue. It
// leaves the single-issue Enter path untouched — this is batches only. Unless
// force is set it first checks the set is mutually parallel-safe (disjoint
// Touches, no blocking edge, known scope); an unsafe set is refused with a hint
// to press capital P to override. Per-issue worktree failures are reported but
// don't abort the rest of the batch.
func (m model) dispatchParallel(force bool) (tea.Model, tea.Cmd) {
	sel := m.selectedIssues()
	if len(sel) == 0 {
		m.status = "select issues with space, then p to run them in parallel"
		return m, nil
	}
	if os.Getenv("TMUX") == "" {
		m.status = "can't spawn: capataz isn't in tmux"
		return m, nil
	}
	if !force && !issues.MutuallyParallelSafe(sel) {
		m.status = "selected set overlaps — press P to dispatch anyway"
		return m, nil
	}

	dispatched := 0
	for _, iss := range sel {
		num := iss.Num
		if num == "" {
			num = "--"
		}
		leaf := iss.Feature + "-" + num + "-" + iss.Slug
		branch := issues.DispatchBranch(iss)
		dir := filepath.Join(paths.WorktreesDir(), repoWorktreeID(m.issueRepo), leaf)

		if err := worktree.Add(m.issueRepo, dir, branch); err != nil {
			m.status = "worktree for " + num + " failed: " + err.Error()
			continue // don't abort the batch — dispatch the rest
		}
		name := iss.Feature + "-" + num
		if _, err := newSessionWithSidebar(name, dir, worktreeDispatchPrompt(iss.Path, branch)); err != nil {
			m.status = "session for " + num + " failed: " + err.Error()
			continue
		}
		dispatched++
	}

	m.issueSelected = nil // clear the selection after a dispatch
	m.mode = modeNormal
	if dispatched > 0 {
		m.status = fmt.Sprintf("dispatched %d parallel session(s)", dispatched)
		if m.popup {
			return m, tea.Quit
		}
	}
	return m, nil
}

// mergeReady merges each ready dispatched worktree branch (Round 2) into the
// primary checkout's CURRENT branch, in turn, without switching branches:
//
//   - clean merge: tear down the merged worktree + branch (RemoveMerged) and
//     count it.
//   - conflict (worktree.ErrConflict): STOP — leave the conflicted merge in the
//     working tree and spawn a fresh session in the primary checkout to resolve
//     it via the /capataz-fix-conflict skill. The conflicted worktree is left in
//     place so the fix agent can reference it.
//   - any other error: STOP with a clear status (the tree may be mid-merge).
//
// It reads the render-safe cache (readyMerges); a fresh scan happens on the next
// forced tick. Plain merges run even outside tmux — only the fix-conflict spawn
// needs tmux and degrades to a status message when $TMUX is unset.
func (m model) mergeReady() (tea.Model, tea.Cmd) {
	if len(m.readyMerges) == 0 {
		m.status = "nothing ready to merge (resolve dispatched issues first)"
		return m, nil
	}
	merged := 0
	for _, c := range m.readyMerges {
		err := worktree.Merge(m.issueRepo, c.Branch)
		switch {
		case err == nil:
			if rmErr := worktree.RemoveMerged(m.issueRepo, c.Path, c.Branch); rmErr != nil {
				m.status = fmt.Sprintf("merged %s but cleanup failed: %v", c.Branch, rmErr)
				m.mode = modeNormal
				return m, nil
			}
			merged++
		case errors.Is(err, worktree.ErrConflict):
			return m.resolveConflict(c, merged)
		default:
			m.status = fmt.Sprintf("merge of %s failed: %v", c.Branch, err)
			m.mode = modeNormal
			return m, nil
		}
	}
	m.status = fmt.Sprintf("merged %d branch(es)", merged)
	m.mode = modeNormal
	return m, nil
}

// resolveConflict handles a conflicted merge mid-batch: it leaves the conflict
// in the working tree and spawns a fresh session in the primary checkout running
// the /capataz-fix-conflict skill to resolve, build, and commit the merge. merged
// is how many branches landed cleanly before this one, for the status line.
func (m model) resolveConflict(c mergeCandidate, merged int) (tea.Model, tea.Cmd) {
	leaf := strings.TrimPrefix(c.Branch, issues.BranchPrefix)
	if os.Getenv("TMUX") == "" {
		m.status = fmt.Sprintf("conflict merging %s — resolve it manually (capataz isn't in tmux)", c.Branch)
		m.mode = modeNormal
		return m, nil
	}
	prompt := fmt.Sprintf("Use the /capataz-fix-conflict skill: a merge of branch %s into the base in this repo is in progress and conflicted. Resolve the conflicts, run the build/tests, and commit the merge.", c.Branch)
	pane, err := newSessionWithSidebar("fixconflict-"+leaf, m.issueRepo, prompt)
	if err != nil {
		m.status = fmt.Sprintf("conflict merging %s and couldn't spawn resolver: %v", c.Branch, err)
		m.mode = modeNormal
		return m, nil
	}
	m.status = fmt.Sprintf("conflict merging %s — resolving in a fresh session", c.Branch)
	if merged > 0 {
		m.status = fmt.Sprintf("merged %d, then conflict on %s — resolving in a fresh session", merged, c.Branch)
	}
	m.mode = modeNormal
	if jerr := jump(pane); jerr != nil {
		m.status = "spawned resolver but couldn't switch: " + jerr.Error()
		return m, nil
	}
	if m.popup {
		return m, tea.Quit
	}
	return m, nil
}

// worktreeDispatchPrompt is the opening prompt injected into each parallel
// session: it points the agent at the issue's ABSOLUTE path (the .scratch
// backlog is gitignored/absent in the linked worktree) and tells it to commit
// on the branch without merging — capataz merges back later (Round 2).
func worktreeDispatchPrompt(absIssuePath, branch string) string {
	return fmt.Sprintf("Use the /capataz-work skill to work on the issue at %s. You are in an isolated git worktree on branch %s; commit your work on this branch when you resolve (git add -A && git commit) and do not merge — capataz merges it back later.", absIssuePath, branch)
}

// repoWorktreeID builds a single filesystem-safe path segment that disambiguates
// a primary checkout under the shared worktrees dir: the repo's basename plus a
// short hash of its absolute path, so two repos with the same basename don't
// collide.
func repoWorktreeID(repo string) string {
	sum := sha1.Sum([]byte(repo))
	base := sanitizeName(filepath.Base(strings.TrimRight(repo, "/")))
	if base == "" {
		base = "repo"
	}
	return fmt.Sprintf("%s-%x", base, sum[:4])
}

func (m model) handleStartWorkKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.mode = modeIssues // back to the list
		return m, nil
	case "n": // spawn a new claude session on the issue
		if os.Getenv("TMUX") == "" {
			m.status = "can't spawn: capataz isn't in tmux"
			m.mode = modeNormal
			return m, nil
		}
		name := m.startIssue.Slug
		if name == "" {
			name = m.startIssue.Feature
		}
		pane, err := newSessionWithSidebar(name, m.issueRepo, m.issuePrompt())
		if err != nil {
			m.status = "new session failed: " + err.Error()
			m.mode = modeNormal
			return m, nil
		}
		m.status = "started " + m.startIssue.Title
		m.mode = modeNormal
		if err := jump(pane); err != nil {
			m.status = "spawned but couldn't switch: " + err.Error()
			return m, nil
		}
		if m.popup {
			return m, tea.Quit
		}
		return m, nil
	case "h": // hand the issue to the currently-selected running session
		// Resolve the target pane LIVE, at action time — not when the chooser
		// opened. The chooser is modal (selection can't change underneath it),
		// but the daemon's ~5s sweep can prune a session whose pane died between
		// open and now; reading m.current() here means we never reply() to a
		// pane the daemon has already dropped.
		sel, ok := m.current()
		if !ok || sel.TmuxPane == "" {
			m.status = "selected session's pane is gone"
			m.mode = modeNormal
			return m, nil
		}
		pane := sel.TmuxPane
		if err := reply(pane, m.issuePrompt()); err != nil {
			m.status = "hand-off failed: " + err.Error()
			m.mode = modeNormal
			return m, nil
		}
		m.status = "sent " + m.startIssue.Title + " to " + m.issueRepoName
		m.mode = modeNormal
		// Match the "selecting acts and quits" popup contract (like the n
		// branch and normal Enter): switch the client to the session that
		// received the work, then dismiss the popup. The sidebar stays open.
		if err := jump(pane); err != nil {
			m.status = "sent but couldn't switch: " + err.Error()
			return m, nil
		}
		if m.popup {
			return m, tea.Quit
		}
		return m, nil
	}
	return m, nil
}

// issuePrompt is the instruction injected into the dispatched session (both the
// new-session and hand-off paths). The session acts as an ORCHESTRATOR: it
// delegates the actual work to a subagent so its own main-thread context stays
// clean. capataz shows that subagent as a ⊂N count on the session's row while
// it works. The path is relative to the repo (the session runs there), falling
// back to absolute. When the sanity-check option is on, the check runs at the
// orchestrator level (a second subagent) — subagents can't nest, so it can't
// live inside the implementation subagent.
func (m model) issuePrompt() string {
	path := m.startIssue.Path
	if rel, err := filepath.Rel(m.issueRepo, m.startIssue.Path); err == nil && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	prompt := fmt.Sprintf("Orchestrate the issue at %s: use the Agent tool to spawn a subagent that works it end to end with the /capataz-work skill (claim, implement, build/test, resolve). Don't implement in this thread — delegate to the subagent, then relay its result; if it reports blocked or needs-info, surface that.", path)
	if conf().Review.SanityCheck {
		prompt += " Sanity check: have the implementation subagent stop after implementing (before it resolves) and hand back its diff, then spawn a separate fresh subagent to sanity-check that diff against the acceptance criteria; only resolve once it passes."
	}
	return prompt
}

// ---- rendering ----

// issueBadgeParts maps a Status string to a short label and color for the badge.
func issueBadgeParts(status string) (string, lipgloss.Color) {
	switch status {
	case "ready-for-agent":
		return "READY", lipgloss.Color("42") // green
	case "claimed":
		return "CLAIMED", lipgloss.Color("214") // amber
	case "resolved", "done", "closed":
		return "DONE", lipgloss.Color("240") // dim
	case "wontfix":
		return "WONTFIX", lipgloss.Color("240")
	case "needs-info":
		return "NEEDS-INFO", lipgloss.Color("208") // orange
	case "needs-triage":
		return "TRIAGE", lipgloss.Color("244")
	case "ready-for-human":
		return "HUMAN", lipgloss.Color("39") // cyan
	case "":
		return "—", lipgloss.Color("244")
	default:
		return strings.ToUpper(status), lipgloss.Color("244")
	}
}

func issueBadge(status string) string {
	label, color := issueBadgeParts(status)
	if ansi.StringWidth(label) > 10 {
		label = ansi.Truncate(label, 10, "")
	}
	return lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%-10s", label))
}

// issuesPopup renders the backlog as a selectable, feature-grouped list scoped
// to the selected session's repo.
func (m model) issuesPopup(base string) string {
	pw, ph, px, py := m.popupDims()
	inner := pw - 2
	if inner < 1 {
		inner = 1
	}
	rows := ph - 2
	if rows < 2 {
		rows = 2
	}
	contentRows := rows - 1 // reserve the last row for the footer help
	if contentRows < 1 {
		contentRows = 1
	}

	help := footerStyle.Render("↵ start work · space select · p run parallel · R review · Esc close")
	if m.issueCursor >= 0 && m.issueCursor < len(m.issueList) {
		if f := m.issueList[m.issueCursor].Feature; m.featureAllDone(f) {
			help = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("✓ "+f+" all resolved") +
				footerStyle.Render(" · R for a fresh review · Esc close")
		}
	}
	if adv := parallelSafeAdvisory(m.issueList); adv != "" {
		help = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(adv) +
			footerStyle.Render(" · ") + help
	}
	if n := len(m.readyMerges); n > 0 {
		ready := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(fmt.Sprintf("%d ready to merge · m", n))
		help = ready + footerStyle.Render(" · ") + help
	}
	title := "Issues · " + m.issueRepoName

	var body string
	if len(m.issueList) == 0 {
		lines := padTo([]string{"", dimStyle.Render("  No .scratch backlog in this repo."), dimStyle.Render("  Create issues with /to-issues, then reopen (i).")}, contentRows)
		body = strings.Join(lines, "\n") + "\n" + help
	} else {
		lines, cursorLine := m.issueLines()
		lines = windowAround(lines, cursorLine, contentRows)
		lines = padTo(lines, contentRows)
		body = strings.Join(lines, "\n") + "\n" + help
	}

	box := panel(title, body, pw, ph, true)
	return overlay(base, box, px, py)
}

// issueLines builds the full (unwindowed) display lines and the index of the
// line holding the cursor. Feature headers are interleaved when the feature
// changes; only issue lines are selectable.
func (m model) issueLines() ([]string, int) {
	var lines []string
	cursorLine := 0
	lastFeature := ""
	for i, iss := range m.issueList {
		if iss.Feature != lastFeature {
			if len(lines) > 0 {
				lines = append(lines, "") // blank between features
			}
			lines = append(lines, accentStyle.Bold(true).Render(iss.Feature+"/"))
			lastFeature = iss.Feature
		}
		selected := i == m.issueCursor
		if selected {
			cursorLine = len(lines)
		}
		lines = append(lines, m.issueRow(iss, selected))
	}
	return lines, cursorLine
}

// issueRow formats one issue line: a cursor bar, a multi-select checkbox, the
// number, status badge, title, and a blocked marker. Column math is left to
// fitCell (panic-safe padding).
func (m model) issueRow(iss issues.Issue, selected bool) string {
	num := iss.Num
	if num == "" {
		num = "--"
	}
	// Multi-select checkbox: filled when this issue is in the dispatch set.
	box := dimStyle.Render("☐")
	if m.issueSelected[iss.Path] {
		box = accentStyle.Render("☑")
	}
	content := fmt.Sprintf("%s %s %s %s", box, dimStyle.Render(num), issueBadge(iss.Status), iss.Title)
	if iss.Blocked {
		content += lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("  ⛔ blocked")
	}
	bar := " "
	if selected {
		bar = accentBarStyle.Render("▎")
	}
	return bar + content
}

// featureAllDone reports whether every cached issue in a feature is done (and
// there is at least one) — the signal to offer a fresh full review.
func (m model) featureAllDone(feature string) bool {
	seen := false
	for _, iss := range m.issueList {
		if iss.Feature != feature {
			continue
		}
		seen = true
		if issues.Bucket(iss) != "done" {
			return false
		}
	}
	return seen
}

// bucketColor is the tracker color for each status bucket (see issues.Bucket).
func bucketColor(bucket string) lipgloss.Color {
	switch bucket {
	case "blocked":
		return lipgloss.Color("196") // red
	case "doing":
		return lipgloss.Color("214") // amber
	case "done":
		return lipgloss.Color("240") // dim
	default: // todo
		return lipgloss.Color("39") // cyan
	}
}

// bucketOrder sorts the tracker so what needs attention floats to the top and
// finished work sinks to the bottom.
var bucketOrder = map[string]int{"blocked": 0, "todo": 1, "doing": 2, "done": 3}

// issuePanelBody renders the cached backlog as the concise sidebar tracker: a
// one-line status summary, then issues sorted attention-first, each a colored
// dot + number + title. Read-only and cache-only — never scans. Interaction
// (starting work) lives in the prefix+g popup, not here.
func (m model) issuePanelBody(rows int) string {
	if rows < 1 {
		rows = 1
	}
	if len(m.issueList) == 0 {
		return dimStyle.Render("  No .scratch backlog. Create with /to-issues.")
	}

	counts := map[string]int{}
	for _, iss := range m.issueList {
		counts[issues.Bucket(iss)]++
	}
	lines := []string{issueSummary(counts)}
	// Advisory parallel-safe line, only when it fits (summary + advisory + at
	// least one issue row). Cache-only via parallelSafeAdvisory.
	if rows >= 3 {
		if adv := parallelSafeAdvisory(m.issueList); adv != "" {
			lines = append(lines, " "+lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(adv))
		}
	}

	sorted := make([]issues.Issue, len(m.issueList))
	copy(sorted, m.issueList)
	sort.SliceStable(sorted, func(i, j int) bool {
		return bucketOrder[issues.Bucket(sorted[i])] < bucketOrder[issues.Bucket(sorted[j])]
	})

	avail := rows - len(lines) // header lines (summary + optional advisory) taken
	if avail < 0 {
		avail = 0
	}
	overflow := len(sorted) > avail
	show := avail
	if overflow {
		show = avail - 1 // leave a row for the "+N more" hint
	}
	if show > len(sorted) {
		show = len(sorted)
	}
	if show < 0 {
		show = 0
	}
	for i := 0; i < show; i++ {
		lines = append(lines, issueTrackerRow(sorted[i]))
	}
	if overflow {
		lines = append(lines, dimStyle.Render(fmt.Sprintf("  … +%d more · prefix+g to work", len(sorted)-show)))
	}
	return strings.Join(lines, "\n")
}

// parallelSafeAdvisory returns a short, read-only advisory naming the ready
// issues that can currently run in parallel — the largest wave of pairwise
// file-disjoint, mutually-unblocked "todo" issues (issues.ParallelSafe). It
// reads only the passed cache slice (never the filesystem) and returns "" when
// no two ready issues are safe together, so callers can omit the line. Numbers
// are capped so the string can't blow past the panel width.
func parallelSafeAdvisory(list []issues.Issue) string {
	waves := issues.ParallelSafe(list)
	var best []issues.Issue
	for _, w := range waves {
		if len(w) > len(best) {
			best = w
		}
	}
	if len(best) < 2 {
		return "" // nothing worth advising
	}
	const maxShow = 6
	var nums []string
	for _, iss := range best {
		num := iss.Num
		if num == "" {
			num = "--"
		}
		nums = append(nums, num)
		if len(nums) >= maxShow {
			break
		}
	}
	label := strings.Join(nums, ", ")
	if len(best) > maxShow {
		label += fmt.Sprintf(" +%d", len(best)-maxShow)
	}
	return "⇉ parallel-safe: " + label
}

// issueSummary is the colored counts line, e.g. "3 todo · 1 blocked · 2 done".
// Zero buckets are omitted; order is attention-first.
func issueSummary(counts map[string]int) string {
	var parts []string
	for _, b := range []string{"blocked", "todo", "doing", "done"} {
		if n := counts[b]; n > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(bucketColor(b)).Render(fmt.Sprintf("%d %s", n, b)))
		}
	}
	if len(parts) == 0 {
		return dimStyle.Render("  no issues")
	}
	return " " + strings.Join(parts, dimStyle.Render(" · "))
}

// issueTrackerRow is one concise tracker line: a bucket-colored dot, the issue
// number, and the title (dimmed when done). Column fit is left to fitCell.
func issueTrackerRow(iss issues.Issue) string {
	bucket := issues.Bucket(iss)
	dot := lipgloss.NewStyle().Foreground(bucketColor(bucket)).Render("●")
	title := iss.Title
	if bucket == "done" {
		title = dimStyle.Render(title)
	}
	num := iss.Num
	if num == "" {
		num = "--"
	}
	return fmt.Sprintf(" %s %s %s", dot, dimStyle.Render(num), title)
}

// startWorkPopup asks how to start the chosen issue: a new session or handing it
// to the currently-selected running session.
func (m model) startWorkPopup(base string) string {
	handLine := dimStyle.Render("  h  hand to a running session (none selected)")
	if sel, ok := m.current(); ok && sel.TmuxPane != "" {
		handLine = "  " + accentStyle.Render("h") + "  hand to " + accentStyle.Render(m.issueRepoName) + " (selected session)"
	}
	lines := []string{
		"Start " + accentStyle.Bold(true).Render(truncate(m.startIssue.Title, 40)) + "?",
		"",
		"  " + accentStyle.Render("n") + "  new claude session in " + dimStyle.Render(m.issueRepoName),
		handLine,
		"",
		dimStyle.Render("  The agent runs /capataz-work: claim → implement → resolve."),
		"",
		footerStyle.Render("  n / h choose · Esc back"),
	}
	body := strings.Join(lines, "\n")
	pw := 60
	if pw > m.width-2 {
		pw = m.width - 2
	}
	ph := len(lines) + 4
	if ph > m.height-2 {
		ph = m.height - 2
	}
	px := (m.width - pw) / 2
	py := (m.height - ph) / 2
	box := panel("Start work", body, pw, ph, true)
	return overlay(base, box, px, py)
}

// windowAround returns at most n lines from lines, sliding the window so that
// the line at idx stays visible.
func windowAround(lines []string, idx, n int) []string {
	if len(lines) <= n {
		return lines
	}
	start := idx - n/2
	if start < 0 {
		start = 0
	}
	if start+n > len(lines) {
		start = len(lines) - n
	}
	return lines[start : start+n]
}

// padTo pads a slice of lines with blanks up to n (never truncates).
func padTo(lines []string, n int) []string {
	for len(lines) < n {
		lines = append(lines, "")
	}
	return lines
}
