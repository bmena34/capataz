package issueview

// Rendering helpers: theme styles, the bordered panel, status badges, and the
// summary/advisory lines.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/bmena34/capataz/internal/issues"
)

// Theme: magenta accent, rounded borders, gray inactive chrome.
var (
	accent        = lipgloss.Color("212") // magenta/pink
	inactiveColor = lipgloss.Color("240") // gray chrome
	mutedColor    = lipgloss.Color("244")

	accentStyle    = lipgloss.NewStyle().Foreground(accent)
	accentBarStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	activeBorder   = lipgloss.NewStyle().Foreground(accent)
	inactiveBorder = lipgloss.NewStyle().Foreground(inactiveColor)
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	footerStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

// panel renders a rounded box of exactly w×h cells with the title embedded in
// the top border. The body is wrapped/truncated to fit; the border glows magenta
// when active.
func panel(title, body string, w, h int, active bool) string {
	if w < 4 {
		w = 4
	}
	if h < 3 {
		h = 3
	}
	inner := w - 2
	rows := h - 2

	bs := inactiveBorder
	ts := lipgloss.NewStyle().Foreground(mutedColor)
	if active {
		bs = activeBorder
		ts = accentStyle.Bold(true)
	}

	// Top border with embedded title: ╭─ Title ───────╮
	label := "─ " + title + " "
	if ansi.StringWidth(label) > inner {
		label = ansi.Truncate(label, inner, "…")
	}
	fill := inner - ansi.StringWidth(label)
	if fill < 0 {
		fill = 0
	}
	top := bs.Render("╭") + ts.Render(label) + bs.Render(strings.Repeat("─", fill)+"╮")

	var b strings.Builder
	b.WriteString(top + "\n")
	v := bs.Render("│")
	for _, ln := range fitLines(body, inner, rows) {
		b.WriteString(v + ln + v + "\n")
	}
	b.WriteString(bs.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// fitLines returns exactly rows lines, each exactly w display cells wide.
func fitLines(body string, w, rows int) []string {
	raw := strings.Split(body, "\n")
	out := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		if i < len(raw) {
			out = append(out, fitCell(raw[i], w))
		} else {
			out = append(out, strings.Repeat(" ", w))
		}
	}
	return out
}

// fitCell pads or truncates a (possibly ANSI-styled) string to exactly w cells.
func fitCell(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw > w {
		return ansi.Truncate(s, w, "…")
	}
	if sw < w {
		return s + strings.Repeat(" ", w-sw)
	}
	return s
}

func issueBadge(status string) string {
	label, color := issueBadgeParts(status)
	if ansi.StringWidth(label) > 10 {
		label = ansi.Truncate(label, 10, "")
	}
	return lipgloss.NewStyle().Foreground(color).Render(fmt.Sprintf("%-10s", label))
}

// issueBadgeParts maps a Status string to a short label and color for the badge.
// The label comes from the shared vocabulary table (issues.StatusInfo) so labels
// stay in one place; color lives here (internal/issues must not import lipgloss).
// A known status picks its color by constant; the empty and unknown statuses
// fall through to the tolerant fallback (— / uppercased, dim) so Status is never
// a closed enum.
func issueBadgeParts(status string) (string, lipgloss.Color) {
	_, label, known := issues.StatusInfo(status)
	if known {
		return label, statusColor(status)
	}
	if status == "" {
		return "—", lipgloss.Color("244")
	}
	return strings.ToUpper(status), lipgloss.Color("244") // unknown: dim fallback
}

// bucketColor is the tracker color for each status bucket (see issues.Bucket).
func bucketColor(bucket string) lipgloss.Color {
	switch bucket {
	case issues.BucketBlocked:
		return lipgloss.Color("196") // red
	case issues.BucketDoing:
		return lipgloss.Color("214") // amber
	case issues.BucketDone:
		return lipgloss.Color("240") // dim
	default: // todo
		return lipgloss.Color("39") // cyan
	}
}

// issueSummary is the colored counts line, e.g. "3 todo · 1 blocked · 2 done".
// Zero buckets are omitted; order is attention-first.
func issueSummary(counts map[string]int) string {
	var parts []string
	for _, b := range []string{issues.BucketBlocked, issues.BucketTodo, issues.BucketDoing, issues.BucketDone} {
		if n := counts[b]; n > 0 {
			parts = append(parts, lipgloss.NewStyle().Foreground(bucketColor(b)).Render(fmt.Sprintf("%d %s", n, b)))
		}
	}
	if len(parts) == 0 {
		return dimStyle.Render("  no issues")
	}
	return " " + strings.Join(parts, dimStyle.Render(" · "))
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

func short(cwd string) string {
	if cwd == "" {
		return "?"
	}
	return filepath.Base(cwd)
}

// statusColor is the badge color for each known status, keyed by the shared
// issues.Status* constants. Color is TUI-only (see issueBadgeParts).
func statusColor(status string) lipgloss.Color {
	switch status {
	case issues.StatusReadyForAgent:
		return lipgloss.Color("42") // green
	case issues.StatusClaimed:
		return lipgloss.Color("214") // amber
	case issues.StatusResolved, issues.StatusDone, issues.StatusClosed, issues.StatusWontfix:
		return lipgloss.Color("240") // dim
	case issues.StatusNeedsInfo:
		return lipgloss.Color("208") // orange
	case issues.StatusAsking:
		return lipgloss.Color("201") // magenta: waiting on the operator
	case issues.StatusReadyForHuman:
		return lipgloss.Color("39") // cyan
	default: // StatusNeedsTriage and any other known status without a distinct color
		return lipgloss.Color("244")
	}
}
