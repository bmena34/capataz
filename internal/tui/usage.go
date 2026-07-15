package tui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/bmena34/capataz/internal/proto"
)

// modelWindows is the built-in fallback table of context-window sizes, keyed by
// a substring of the (lowercased) model id. It covers models whose base window
// differs from the 200k default; matched by substring so future point releases
// still resolve. The transcript never carries the [1m] beta marker, so the id
// alone can't reveal a per-session beta opt-in — this table only seeds models
// Claude Code offers in a 1M form by default. Where a model ships in both a
// 200k and a 1M variant under one id (e.g. Sonnet 4.6, "Sonnet" vs "Sonnet (1M
// context)"), it is NOT seeded here — that would overstate the 200k case — and
// the 1M user opts in via config (Context.Limits), which takes precedence.
var modelWindows = map[string]int{
	// Claude Code offers Opus 4.8 and Fable 5 only in a 1M-context form, so their
	// bare ids always mean 1M. Overridable via Context.Limits.
	"claude-opus-4-8": 1_000_000,
	"claude-fable-5":  1_000_000,
}

// contextLimit returns the context-window size for a session. Order of
// preference: (1) CAPATAZ_CONTEXT_LIMIT env override; (2) a config override in
// conf().Context.Limits, resolved by most-specific match — the longest key that
// equals or is a substring of the lowercased model wins, so an exact match beats
// any substring and a specific key (e.g. "claude-opus-4-8") beats a broad one
// (e.g. "claude"). Map iteration order is randomized, so we pick the longest
// matching key rather than the first visited, making the result deterministic
// regardless of how many keys overlap. (3) the built-in modelWindows table;
// (4) the 200k default. As a final harmless fallback, a "1m" hint in the model
// name maps to 1M. The tokens param is kept for caller/signature stability but
// no longer drives an auto-jump.
func contextLimit(model string, tokens int) int {
	if env := os.Getenv("CAPATAZ_CONTEXT_LIMIT"); env != "" {
		if n := parseTokenCount(env); n > 0 {
			return n
		}
	}
	lower := strings.ToLower(model)
	// Pick the most-specific (longest) key that matches, so overlapping keys
	// resolve deterministically regardless of map iteration order.
	bestLen, bestVal := -1, 0
	for key, val := range conf().Context.Limits {
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "" {
			continue
		}
		if k == lower || strings.Contains(lower, k) {
			if n := parseTokenCount(val); n > 0 && len(k) > bestLen {
				bestLen, bestVal = len(k), n
			}
		}
	}
	if bestLen >= 0 {
		return bestVal
	}
	for key, n := range modelWindows {
		if strings.Contains(lower, key) {
			return n
		}
	}
	if strings.Contains(lower, "1m") {
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
