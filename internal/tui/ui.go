package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Theme: magenta accent, rounded borders, gray inactive chrome.
var (
	accent        = lipgloss.Color("212") // magenta/pink
	inactiveColor = lipgloss.Color("240") // gray chrome
	mutedColor    = lipgloss.Color("244")

	accentStyle    = lipgloss.NewStyle().Foreground(accent)
	accentBarStyle = lipgloss.NewStyle().Foreground(accent).Bold(true)
	selRowStyle    = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	activeBorder   = lipgloss.NewStyle().Foreground(accent)
	inactiveBorder = lipgloss.NewStyle().Foreground(inactiveColor)
	keybarStyle    = lipgloss.NewStyle().Foreground(mutedColor)
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

// overlay composites fg on top of bg at (x, y), preserving the background that
// shows around the floating window. ANSI-aware so styled cells aren't broken.
func overlay(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")
	for i, fl := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		bl := bgLines[row]
		fgW := ansi.StringWidth(fl)

		left := ansi.Truncate(bl, x, "")
		if lw := ansi.StringWidth(left); lw < x {
			left += strings.Repeat(" ", x-lw)
		}
		right := ansi.TruncateLeft(bl, x+fgW, "")

		bgLines[row] = left + "\x1b[0m" + fl + "\x1b[0m" + right
	}
	return strings.Join(bgLines, "\n")
}
