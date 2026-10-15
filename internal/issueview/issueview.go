package issueview

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/worktree"
)

// Run launches a full-screen, read-only backlog viewer for the repo containing
// cwd (resolved to its primary checkout). It reads the .scratch backlog straight
// off disk and refreshes on a cheap mtime probe.
func Run(cwd string) error {
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	repo := cwd
	if p, err := worktree.PrimaryCheckout(cwd); err == nil {
		repo = p // the backlog lives in the primary checkout
	}
	m := issueViewModel{repo: repo, repoName: short(repo)}
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// issueViewModel is the standalone viewer's Bubble Tea model. It holds only what
// a read-only backlog needs: the scanned list, a cursor, terminal size, and the
// last .scratch mtime so the tick can skip an unchanged rescan.
type issueViewModel struct {
	repo     string
	repoName string
	list     []issues.Issue
	cursor   int
	w, h     int
	mod      time.Time
	loadErr  error
}

type ivLoadedMsg struct {
	list []issues.Issue
	mod  time.Time
	err  error
}

type ivTickMsg struct{}

func (m issueViewModel) Init() tea.Cmd {
	return tea.Batch(loadIssuesCmd(m.repo), ivTick())
}

// loadIssuesCmd scans the repo's backlog off the render path and flattens the
// features into a single list in Scan order.
func loadIssuesCmd(repo string) tea.Cmd {
	return func() tea.Msg {
		mod, _ := issues.ScratchModTime(repo)
		feats, err := issues.Scan(repo)
		if err != nil {
			return ivLoadedMsg{err: err}
		}
		var list []issues.Issue
		for _, f := range feats {
			list = append(list, f.Issues...)
		}
		return ivLoadedMsg{list: list, mod: mod}
	}
}

func ivTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return ivTickMsg{} })
}

func (m issueViewModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil
	case ivLoadedMsg:
		m.loadErr = msg.err
		if msg.err == nil {
			m.list = msg.list
			m.mod = msg.mod
			m.clamp()
		}
		return m, nil
	case ivTickMsg:
		// Cheap stat-only probe: rescan only when .scratch changed since the last
		// load (or nothing has loaded yet), so an agent's claim/resolve shows up
		// without rereading the tree every tick. Always re-arm the ticker.
		cmds := []tea.Cmd{ivTick()}
		if mod, err := issues.ScratchModTime(m.repo); err != nil || m.mod.IsZero() || !mod.Equal(m.mod) {
			cmds = append(cmds, loadIssuesCmd(m.repo))
		}
		return m, tea.Batch(cmds...)
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			m.cursor++
			m.clamp()
		case "k", "up":
			m.cursor--
			m.clamp()
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = len(m.list) - 1
			m.clamp()
		case "r":
			return m, loadIssuesCmd(m.repo)
		}
		return m, nil
	}
	return m, nil
}

func (m *issueViewModel) clamp() {
	if len(m.list) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.list) {
		m.cursor = len(m.list) - 1
	}
}

func (m issueViewModel) View() string {
	if m.w < 1 || m.h < 1 {
		return ""
	}
	rows := m.h - 2 // panel eats a border row top and bottom
	if rows < 2 {
		rows = 2
	}
	contentRows := rows - 1 // reserve the last row for the footer
	if contentRows < 1 {
		contentRows = 1
	}

	footer := m.footer()

	var body string
	switch {
	case m.loadErr != nil:
		lines := padTo([]string{"", dimStyle.Render("  issue scan failed: " + m.loadErr.Error())}, contentRows)
		body = strings.Join(lines, "\n") + "\n" + footer
	case len(m.list) == 0:
		lines := padTo([]string{
			"",
			dimStyle.Render("  No .scratch backlog in this repo."),
			dimStyle.Render("  Create issues with /capataz-splitter, then reopen."),
		}, contentRows)
		body = strings.Join(lines, "\n") + "\n" + footer
	default:
		lines, cursorLine := issueViewLines(m.list, m.cursor)
		lines = windowAround(lines, cursorLine, contentRows)
		lines = padTo(lines, contentRows)
		body = strings.Join(lines, "\n") + "\n" + footer
	}
	return panel("Issues · "+m.repoName, body, m.w, m.h, true)
}

// footer is the status line: attention-first bucket
// counts, the parallel-safe advisory when there is one, then the key hints.
func (m issueViewModel) footer() string {
	help := footerStyle.Render("j/k move · r reload · q quit")
	if len(m.list) == 0 {
		return help
	}
	counts := map[string]int{}
	for _, iss := range m.list {
		counts[issues.Bucket(iss)]++
	}
	left := issueSummary(counts)
	if adv := parallelSafeAdvisory(m.list); adv != "" {
		left += footerStyle.Render(" · ") +
			lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(adv)
	}
	return left + footerStyle.Render("   ") + help
}

// issueViewLines builds the feature-grouped display lines and the index of the
// cursor line. Only issue rows are selectable.
func issueViewLines(list []issues.Issue, cursor int) ([]string, int) {
	var lines []string
	cursorLine := 0
	lastFeature := ""
	for i, iss := range list {
		if iss.Feature != lastFeature {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, accentStyle.Bold(true).Render(iss.Feature+"/"))
			lastFeature = iss.Feature
		}
		selected := i == cursor
		if selected {
			cursorLine = len(lines)
		}
		lines = append(lines, issueViewRow(iss, selected))
	}
	return lines, cursorLine
}

// issueViewRow formats one read-only issue line: a cursor bar, the number, the
// status badge, the title, and a blocked marker. Column fit is left to the panel.
func issueViewRow(iss issues.Issue, selected bool) string {
	num := iss.Num
	if num == "" {
		num = "--"
	}
	content := dimStyle.Render(num) + " " + issueBadge(iss.Status) + " " + iss.Title
	if iss.Blocked {
		content += lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("  ⛔ blocked")
	}
	bar := "  "
	if selected {
		bar = accentBarStyle.Render("▎") + " "
	}
	return bar + content
}
