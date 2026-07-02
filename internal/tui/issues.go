package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/worktree"
)

// syncSelectedIssues keeps the cached backlog (issueList) pointed at the
// selected session's repo. It reloads from disk when the selection moves to a
// different cwd, or always when force is set — the periodic tick forces a
// reload to pick up claim/resolve edits agents make on disk. Reads are cheap
// (one .scratch tree) and happen here, off the render path, so the inline
// sidebar panel and the modal both render from the cache without scanning.
//
// The scan root (issueRepo) is the selected cwd resolved to its primary git
// checkout, so a session running in a linked worktree still shows (and starts
// work against) the shared backlog in the primary. That resolution shells out
// to git, so it runs only when the cwd actually changes — never per tick.
func (m *model) syncSelectedIssues(force bool) {
	sel, ok := m.current()
	if !ok {
		m.issueCwd, m.issueRepo, m.issueRepoName, m.issueList = "", "", "", nil
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
	} else if !force {
		return // same cwd and no forced refresh: keep the cache
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
	case "enter", "o":
		if m.issueCursor >= 0 && m.issueCursor < len(m.issueList) {
			m.startIssue = m.issueList[m.issueCursor]
			m.startPane = ""
			if s, ok := m.current(); ok {
				m.startPane = s.TmuxPane
			}
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
		if m.startPane == "" {
			m.status = "selected session has no tmux pane"
			m.mode = modeNormal
			return m, nil
		}
		if err := reply(m.startPane, m.issuePrompt()); err != nil {
			m.status = "hand-off failed: " + err.Error()
		} else {
			m.status = "sent " + m.startIssue.Title + " to " + m.issueRepoName
		}
		m.mode = modeNormal
		return m, nil
	}
	return m, nil
}

// issuePrompt is the instruction injected into the agent: run the capataz-work
// skill on this issue. The path is relative to the repo (both dispatch targets
// run in the repo), falling back to the absolute path if that can't be computed.
// When the sanity-check option is on, it asks for the pre-resolve subagent pass.
func (m model) issuePrompt() string {
	path := m.startIssue.Path
	if rel, err := filepath.Rel(m.issueRepo, m.startIssue.Path); err == nil && !strings.HasPrefix(rel, "..") {
		path = rel
	}
	prompt := fmt.Sprintf("Use the /capataz-work skill to work on the issue at %s", path)
	if conf().Review.SanityCheck {
		prompt += ". Run the optional sanity-check subagent before resolving."
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

	help := footerStyle.Render("↵ start work · R review · j/k move · Esc close")
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

// issueRow formats one issue line: a select bar, number, status badge, title,
// and a blocked marker. Column math is left to fitCell (panic-safe padding).
func (m model) issueRow(iss issues.Issue, selected bool) string {
	num := iss.Num
	if num == "" {
		num = "--"
	}
	content := fmt.Sprintf("%s %s %s", dimStyle.Render(num), issueBadge(iss.Status), iss.Title)
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
	if m.startPane != "" {
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
