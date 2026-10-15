package usageview

import (
	"strings"
	"testing"
	"time"
)

func TestFmtTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1_234, "1k"},
		{51_000, "51k"},
		{199_999, "199k"},
		{1_000_000, "1.0M"},
		{1_500_000, "1.5M"},
	}
	for _, c := range cases {
		if got := fmtTokens(c.in); got != c.want {
			t.Errorf("fmtTokens(%d): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPctOf(t *testing.T) {
	cases := []struct {
		tokens, limit, want int
	}{
		{0, 200_000, 0},
		{100_000, 200_000, 50},
		{200_000, 200_000, 100},
		{300_000, 200_000, 100}, // clamped — never render an impossible bar
		{50_000, 0, 0},          // unknown limit
		{-5, 200_000, 0},
	}
	for _, c := range cases {
		if got := pctOf(c.tokens, c.limit); got != c.want {
			t.Errorf("pctOf(%d,%d): got %d, want %d", c.tokens, c.limit, got, c.want)
		}
	}
}

func TestBar_FillLevels(t *testing.T) {
	p := painter{on: false}
	cases := []struct {
		pct, width, filled int
	}{
		{0, 10, 0},
		{50, 10, 5},
		{100, 10, 10},
		{150, 10, 10}, // clamped
		{-10, 10, 0},
	}
	for _, c := range cases {
		got := strings.Count(p.bar(c.pct, c.width), "█")
		if got != c.filled {
			t.Errorf("bar(%d%%, %d): got %d filled, want %d", c.pct, c.width, got, c.filled)
		}
	}
}

// A live-but-small session must be visibly different from an empty one, so any
// non-zero reading rounds up to at least one cell.
func TestBar_TinyNonZeroShowsOneCell(t *testing.T) {
	p := painter{on: false}
	if got := strings.Count(p.bar(1, 20), "█"); got != 1 {
		t.Errorf("bar(1%%, 20): got %d filled, want 1", got)
	}
}

func TestBar_TotalWidthIsStable(t *testing.T) {
	p := painter{on: false}
	for pct := 0; pct <= 100; pct += 7 {
		b := p.bar(pct, 12)
		if got := len([]rune(b)); got != 14 { // 12 cells + the two brackets
			t.Errorf("bar(%d%%) width: got %d runes, want 14 (%q)", pct, got, b)
		}
	}
}

func TestTruncateAndPad(t *testing.T) {
	if got := truncate("capataz-herdr", 8); got != "capataz…" {
		t.Errorf("truncate: got %q", got)
	}
	if got := truncate("short", 20); got != "short" {
		t.Errorf("truncate no-op: got %q", got)
	}
	if got := truncate("abc", 0); got != "" {
		t.Errorf("truncate to 0: got %q", got)
	}
	if got := pad("ab", 5); got != "ab   " {
		t.Errorf("pad: got %q", got)
	}
	if got := pad("abcdef", 3); got != "abcdef" {
		t.Errorf("pad no-op: got %q", got)
	}
}

// The plan gauge arrives pre-colored from `capataz usage`. Truncating it must
// measure display cells (not bytes), never split an escape sequence, and never
// leave color bleeding into the rest of the frame.
func TestVisibleLenIgnoresANSI(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"plain", 5},
		{ansiGreen + "green" + ansiReset, 5},
		{ansiBold + "" + ansiReset, 0},
		{"héllo", 5}, // multi-byte runes count as one cell each
		{"[" + ansiRed + "██" + ansiReset + "]", 4},
	}
	for _, c := range cases {
		if got := visibleLen(c.in); got != c.want {
			t.Errorf("visibleLen(%q): got %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateVisible(t *testing.T) {
	if got := truncateVisible("short", 20); got != "short" {
		t.Errorf("no-op truncate: got %q", got)
	}

	colored := ansiGreen + "abcdefghij" + ansiReset
	got := truncateVisible(colored, 5)
	if visibleLen(got) > 5 {
		t.Errorf("truncated to %d visible cells, want <= 5: %q", visibleLen(got), got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected an ellipsis: %q", got)
	}
	if !strings.HasSuffix(got, ansiReset) {
		t.Errorf("colored truncation must end in a reset: %q", got)
	}
	if strings.Count(got, "\033[") < 1 {
		t.Errorf("escape sequence was lost: %q", got)
	}

	// A plain string needs no reset appended.
	if plain := truncateVisible("abcdefghij", 5); strings.Contains(plain, "\033") {
		t.Errorf("plain truncation should not emit escapes: %q", plain)
	}
	if got := truncateVisible("abc", 0); got != "" {
		t.Errorf("truncate to 0: got %q", got)
	}
}

func TestEscapeLen(t *testing.T) {
	if got := escapeLen(ansiGreen + "x"); got != len(ansiGreen) {
		t.Errorf("escapeLen(SGR): got %d, want %d", got, len(ansiGreen))
	}
	if got := escapeLen("\033[0m"); got != 4 {
		t.Errorf("escapeLen(reset): got %d, want 4", got)
	}
	if got := escapeLen("\033"); got != 1 {
		t.Errorf("escapeLen(bare ESC): got %d, want 1", got)
	}
}

// A long plan line must be clipped to the popup rather than wrapping, which
// would push the context table out of the frame.
func TestRenderView_ClipsLongPlanLine(t *testing.T) {
	v := baseView()
	v.Plan = ansiGreen + strings.Repeat("plan-usage-detail ", 12) + ansiReset
	out := renderView(v, 40, 24, false)

	for _, line := range strings.Split(out, "\n") {
		if visibleLen(line) > 40 {
			t.Errorf("line exceeds width 40 (%d cells): %q", visibleLen(line), line)
		}
	}
	if !strings.Contains(out, "Context") {
		t.Error("context section should survive a long plan line")
	}
}

func baseView() View {
	return View{
		Plan:     "Plan: Enterprise / API (billed per token — no rate-limit bars)",
		ShowPlan: true,
		Rows: []Row{
			{Label: "capataz-herdr", Tokens: 51_000, Limit: 1_000_000, Marked: true},
			{Label: "drain-03", Tokens: 148_000, Limit: 200_000},
		},
	}
}

func TestRenderView_Layout(t *testing.T) {
	out := renderView(baseView(), 74, 24, false)

	if !strings.Contains(out, "Plan: Enterprise") {
		t.Error("plan line missing")
	}
	if !strings.Contains(out, "Context") {
		t.Error("context header missing")
	}
	if !strings.Contains(out, "51k / 1.0M") {
		t.Error("marked pane token counts missing")
	}
	if !strings.Contains(out, "148k / 200k") {
		t.Error("second pane token counts missing")
	}
	if !strings.Contains(out, "▸ capataz-herdr") {
		t.Error("marked pane should carry the ▸ marker")
	}
	if !strings.Contains(out, "  5%") || !strings.Contains(out, " 74%") {
		t.Errorf("percentages missing:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if len([]rune(line)) > 74 {
			t.Errorf("line exceeds width 74 (%d): %q", len([]rune(line)), line)
		}
	}
}

func TestRenderView_NoPlanSection(t *testing.T) {
	v := baseView()
	v.ShowPlan = false
	out := renderView(v, 74, 24, false)

	if strings.Contains(out, "Plan:") {
		t.Error("plan section should be omitted with ShowPlan=false")
	}
	if !strings.Contains(out, "Context") {
		t.Error("context section should still render")
	}
}

func TestRenderView_PlanNoteWhenUnavailable(t *testing.T) {
	v := baseView()
	v.Plan = ""
	v.PlanNote = "plan usage unavailable (capataz not found)"
	out := renderView(v, 74, 24, false)

	if !strings.Contains(out, "plan usage unavailable") {
		t.Error("plan note missing")
	}
	// The context gauges must survive a missing capataz — that is the whole
	// point of capataz being an optional dependency here.
	if !strings.Contains(out, "51k / 1.0M") {
		t.Error("context rows should still render without capataz")
	}
}

func TestRenderView_LiveFooter(t *testing.T) {
	v := baseView()
	v.Live = true
	v.Interval = 2 * time.Second
	out := renderView(v, 74, 24, false)

	if !strings.Contains(out, "q quit") || !strings.Contains(out, "refresh 2s") {
		t.Errorf("live footer missing:\n%s", out)
	}
	// A one-shot render has no key to press, so it must not claim otherwise.
	if strings.Contains(renderView(baseView(), 74, 24, false), "q quit") {
		t.Error("static render should not show the quit hint")
	}
}

func TestRenderView_NoteRowsRenderWithoutGauge(t *testing.T) {
	v := View{ShowPlan: false, Rows: []Row{
		{Label: "sidecar", Note: "codex — no context data"},
	}}
	out := renderView(v, 74, 24, false)

	if !strings.Contains(out, "codex — no context data") {
		t.Error("note text missing")
	}
	if strings.Contains(out, "█") || strings.Contains(out, "░") {
		t.Error("a note row must not draw a gauge")
	}
}

func TestRenderView_EmptyAndErrorStates(t *testing.T) {
	empty := renderView(View{ShowPlan: false}, 74, 24, false)
	if !strings.Contains(empty, "no agent panes") {
		t.Errorf("empty state missing:\n%s", empty)
	}

	failed := renderView(View{ShowPlan: false, Err: "herdr agent list: server not running"}, 74, 24, false)
	if !strings.Contains(failed, "server not running") {
		t.Errorf("error state missing:\n%s", failed)
	}
	if strings.Contains(failed, "no agent panes") {
		t.Error("an error must not be reported as an empty list")
	}
}

// The popup is small and the pane count is not: rows beyond the budget collapse
// into a count rather than overflowing the popup.
func TestRenderView_TruncatesToHeightWithNotice(t *testing.T) {
	v := View{ShowPlan: false, Live: true, Interval: 2 * time.Second}
	for i := 0; i < 12; i++ {
		v.Rows = append(v.Rows, Row{Label: "pane", Tokens: 1000, Limit: 200_000})
	}
	out := renderView(v, 74, 8, false)

	if !strings.Contains(out, "more") {
		t.Errorf("expected an overflow notice:\n%s", out)
	}
	if got := len(strings.Split(out, "\n")); got > 8 {
		t.Errorf("frame is %d lines, want <= 8:\n%s", got, out)
	}
}

// Narrow popups drop the bar rather than wrapping the line.
func TestRenderView_NarrowDropsBar(t *testing.T) {
	wide := renderView(baseView(), 74, 24, false)
	if !strings.Contains(wide, "█") && !strings.Contains(wide, "░") {
		t.Error("wide render should include a gauge")
	}

	narrow := renderView(baseView(), 34, 24, false)
	if strings.Contains(narrow, "░") {
		t.Errorf("narrow render should drop the gauge:\n%s", narrow)
	}
	for _, line := range strings.Split(narrow, "\n") {
		if len([]rune(line)) > 34 {
			t.Errorf("line exceeds width 34 (%d): %q", len([]rune(line)), line)
		}
	}
}

func TestRenderView_ColorOnlyWhenEnabled(t *testing.T) {
	if strings.Contains(renderView(baseView(), 74, 24, false), "\033[") {
		t.Error("color disabled but ANSI escapes were emitted")
	}
	if !strings.Contains(renderView(baseView(), 74, 24, true), "\033[") {
		t.Error("color enabled but no ANSI escapes were emitted")
	}
}

func TestLevelColor_Thresholds(t *testing.T) {
	p := painter{on: true}
	cases := []struct {
		pct  int
		want string
	}{
		{0, ansiGreen}, {49, ansiGreen},
		{50, ansiAmber}, {79, ansiAmber},
		{80, ansiRed}, {100, ansiRed},
	}
	for _, c := range cases {
		if got := p.levelColor(c.pct); got != c.want {
			t.Errorf("levelColor(%d): got %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestShortDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{2 * time.Second, "2s"},
		{time.Minute, "1m"},
		{90 * time.Second, "1m30s"},
	}
	for _, c := range cases {
		if got := shortDuration(c.in); got != c.want {
			t.Errorf("shortDuration(%v): got %q, want %q", c.in, got, c.want)
		}
	}
}

func TestOneLine(t *testing.T) {
	if got := oneLine("first line\nsecond line"); got != "first line" {
		t.Errorf("oneLine: got %q", got)
	}
	if got := oneLine("  padded  "); got != "padded" {
		t.Errorf("oneLine trim: got %q", got)
	}
}
