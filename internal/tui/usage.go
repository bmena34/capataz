package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/bmena34/capataz/internal/proto"
)

// contextLimit returns the context-window size for a session. Order of
// preference: CAPATAZ_CONTEXT_LIMIT env override; a "1m" hint in the model name;
// the observed usage already exceeding 200k (so it must be a 1M model);
// otherwise the 200k default.
func contextLimit(model string, tokens int) int {
	if env := os.Getenv("CAPATAZ_CONTEXT_LIMIT"); env != "" {
		if n := parseTokenCount(env); n > 0 {
			return n
		}
	}
	if strings.Contains(strings.ToLower(model), "1m") || tokens > 200_000 {
		return 1_000_000
	}
	return 200_000
}

// parseTokenCount parses values like "1m", "200k", or "1000000".
func parseTokenCount(s string) int {
	s = strings.TrimSpace(strings.ToLower(s))
	mult := 1
	switch {
	case strings.HasSuffix(s, "m"):
		mult, s = 1_000_000, strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "k"):
		mult, s = 1_000, strings.TrimSuffix(s, "k")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int(f * float64(mult))
}

// fmtTokens renders a token count compactly: 1234 -> "1k", 1_500_000 -> "1.5M".
func fmtTokens(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%dk", n/1_000)
	default:
		return strconv.Itoa(n)
	}
}

// pctOf returns the context-usage percentage for a session (0 if unknown).
func pctOf(s proto.Session) int {
	if s.ContextTokens <= 0 {
		return 0
	}
	return int(float64(s.ContextTokens) / float64(contextLimit(s.Model, s.ContextTokens)) * 100)
}

// pctColor picks green/amber/red by fill level.
func pctColor(pct int) lipgloss.Style {
	c := lipgloss.Color("42") // green
	if pct >= 80 {
		c = lipgloss.Color("196") // red
	} else if pct >= 50 {
		c = lipgloss.Color("214") // amber
	}
	return lipgloss.NewStyle().Foreground(c)
}

// bar renders a [████░░░░] usage bar of the given inner width, colored by level.
func bar(pct float64, width int) string {
	if width < 3 {
		width = 3
	}
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	filled := int(pct*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	fill := pctColor(int(pct * 100)).Render(strings.Repeat("█", filled))
	empty := dimStyle.Render(strings.Repeat("░", width-filled))
	return "[" + fill + empty + "]"
}
