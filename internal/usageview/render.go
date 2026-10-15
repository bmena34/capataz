package usageview

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ANSI attributes. The gauge colors match capataz's own TUI (green 42, amber
// 214, red 196) so the popup reads as part of the same tool.
const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiGreen = "\033[38;5;42m"
	ansiAmber = "\033[38;5;214m"
	ansiRed   = "\033[38;5;196m"
)

// painter applies (or strips) ANSI attributes. Tests and piped output render
// with color off, which keeps golden strings readable and honors NO_COLOR.
type painter struct{ on bool }

func (p painter) wrap(attr, s string) string {
	if !p.on || s == "" {
		return s
	}
	return attr + s + ansiReset
}

func (p painter) dim(s string) string  { return p.wrap(ansiDim, s) }
func (p painter) bold(s string) string { return p.wrap(ansiBold, s) }

// levelColor picks green / amber / red by how full the window is, matching the
// thresholds capataz uses (amber at 50%, red at 80%).
func (p painter) levelColor(pct int) string {
	switch {
	case pct >= 80:
		return ansiRed
	case pct >= 50:
		return ansiAmber
	default:
		return ansiGreen
	}
}

// fmtTokens renders a token count compactly: 1234 -> "1k", 1_500_000 -> "1.5M".
func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return strconv.Itoa(n/1_000) + "k"
	default:
		return strconv.Itoa(n)
	}
}

// pctOf returns how full the context window is, 0–100.
func pctOf(tokens, limit int) int {
	if tokens <= 0 || limit <= 0 {
		return 0
	}
	pct := int(float64(tokens) / float64(limit) * 100)
	if pct > 100 {
		pct = 100
	}
	return pct
}

// bar renders a [████░░░░] gauge of the given inner width, colored by level.
func (p painter) bar(pct, width int) string {
	if width < 3 {
		width = 3
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := int(float64(pct)/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	// A non-zero reading always shows at least one cell, so a live-but-small
	// session is visibly different from an empty one.
	if filled == 0 && pct > 0 {
		filled = 1
	}
	fill := p.wrap(p.levelColor(pct), strings.Repeat("█", filled))
	empty := p.dim(strings.Repeat("░", width-filled))
	return "[" + fill + empty + "]"
}

// visibleLen counts display cells, skipping ANSI escape sequences.
func visibleLen(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i += escapeLen(s[i:])
			continue
		}
		_, size := decodeRune(s[i:])
		i += size
		n++
	}
	return n
}

// truncateVisible shortens s to at most n *display* cells while passing ANSI
// escape sequences through untouched (they occupy no width). Used for the plan
// gauge, which arrives pre-colored from `capataz usage` and would otherwise wrap
// and corrupt the frame — naive truncation could also cut an escape in half and
// leak control codes into the pane.
func truncateVisible(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if visibleLen(s) <= n {
		return s
	}
	var b strings.Builder
	cells, hadEscape := 0, false
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			l := escapeLen(s[i:])
			b.WriteString(s[i : i+l])
			hadEscape = true
			i += l
			continue
		}
		if cells >= n-1 { // leave a cell for the ellipsis
			break
		}
		r, size := decodeRune(s[i:])
		b.WriteRune(r)
		i += size
		cells++
	}
	b.WriteString("…")
	if hadEscape {
		b.WriteString(ansiReset) // don't let a cut sequence bleed into later lines
	}
	return b.String()
}

// escapeLen returns the byte length of the ANSI escape sequence starting at the
// front of s (which must begin with ESC), including its final byte.
func escapeLen(s string) int {
	if len(s) < 2 {
		return len(s)
	}
	if s[1] != '[' { // two-byte escape (or something we don't model)
		return 2
	}
	for i := 2; i < len(s); i++ {
		if (s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z') {
			return i + 1
		}
	}
	return len(s)
}

// decodeRune is utf8.DecodeRuneInString, kept behind a name that says why it is
// here: byte-wise iteration must not split multi-byte runes.
func decodeRune(s string) (rune, int) { return utf8.DecodeRuneInString(s) }

// truncate shortens s to at most n display cells, ending with "…" when cut.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// pad right-pads s to n display cells (no-op when already longer).
func pad(s string, n int) string {
	if d := n - len([]rune(s)); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// renderView draws one frame at the given terminal size.
//
// Layout is computed from the actual width rather than assumed, because the
// popup is sized by herdr and by the user's terminal — the bar absorbs whatever
// space is left after the label, the token counts and the percentage, and is
// dropped entirely when the popup is too narrow to hold one.
func renderView(v View, width, height int, color bool) string {
	p := painter{on: color}
	if width < 20 {
		width = 20
	}

	var out []string

	// Plan usage — whatever `capataz usage` printed, verbatim.
	if v.ShowPlan {
		switch {
		case v.Plan != "":
			for _, line := range strings.Split(v.Plan, "\n") {
				out = append(out, truncateVisible(line, width))
			}
		case v.PlanNote != "":
			out = append(out, p.dim(truncate(v.PlanNote, width)))
		default:
			out = append(out, p.dim("plan usage: loading…"))
		}
		out = append(out, "")
	}

	// Context table.
	out = append(out, p.bold("Context"))
	switch {
	case v.Err != "":
		out = append(out, p.wrap(ansiRed, truncate(v.Err, width)))
	case len(v.Rows) == 0:
		out = append(out, p.dim("no agent panes"))
	default:
		out = append(out, renderRows(p, v.Rows, width, height-len(out)-footerLines(v))...)
	}

	if v.Live {
		out = append(out, "")
		out = append(out, p.dim(fmt.Sprintf("q quit · refresh %s", shortDuration(v.Interval))))
	}
	return strings.Join(out, "\n")
}

// footerLines is how many lines renderView reserves at the bottom, so the row
// budget can be computed before the footer is appended.
func footerLines(v View) int {
	if v.Live {
		return 2 // blank separator + hint line
	}
	return 0
}

// renderRows lays out the per-pane gauges, fitting them to width and to the
// number of lines still available.
func renderRows(p painter, rows []Row, width, avail int) []string {
	// Column widths, measured from the data.
	labelW, countW := 0, 0
	for _, r := range rows {
		if n := len([]rune(r.Label)); n > labelW {
			labelW = n
		}
		if n := len(countText(r)); n > countW {
			countW = n
		}
	}
	if labelW > 24 {
		labelW = 24
	}

	// marker(2) + label + gap(2) + counts + gap(1) + "[" + bar + "]" + gap(1) + pct(4)
	// barW is the gauge's *inner* width; the two brackets are charged separately.
	barW := width - 2 - labelW - 2 - countW - 1 - 2 - 1 - 4
	showBar := barW >= 6
	if barW > 24 {
		barW = 24
	}

	// Reserve one line for the "+N more" notice when the list won't fit.
	shown := rows
	overflow := 0
	if avail > 0 && len(rows) > avail {
		keep := avail - 1
		if keep < 0 {
			keep = 0
		}
		shown, overflow = rows[:keep], len(rows)-keep
	}

	out := make([]string, 0, len(shown)+1)
	for _, r := range shown {
		marker := "  "
		if r.Marked {
			marker = "▸ "
		}
		label := pad(truncate(r.Label, labelW), labelW)
		if r.Marked {
			label = p.bold(label)
		}

		line := marker + label + "  "
		if r.Note != "" {
			// Unknown context: say why, in the space the gauge would have used.
			out = append(out, line+p.dim(truncate(r.Note, width-len([]rune(marker))-labelW-2)))
			continue
		}

		pct := pctOf(r.Tokens, r.Limit)
		line += p.dim(pad(countText(r), countW)) + " "
		if showBar {
			line += p.bar(pct, barW) + " "
		}
		line += p.wrap(p.levelColor(pct), fmt.Sprintf("%3d%%", pct))
		out = append(out, line)
	}
	if overflow > 0 {
		out = append(out, p.dim(fmt.Sprintf("  +%d more", overflow)))
	}
	return out
}

// countText is a row's "51k / 1.0M" token reading.
func countText(r Row) string {
	if r.Note != "" {
		return ""
	}
	return fmtTokens(r.Tokens) + " / " + fmtTokens(r.Limit)
}

// shortDuration renders a refresh interval compactly: Go prints a whole minute
// as "1m0s", which reads oddly in a status line, so the dangling zero unit is
// trimmed ("1m0s" -> "1m").
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}
