package tui

import (
	"testing"

	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/proto"
)

func TestRenderNoPanic(t *testing.T) {
	m := model{}
	m.sessions = []proto.Session{
		{ID: "a", TmuxPane: "%1", State: proto.StateWaiting, Cwd: "/Users/x/proj", ContextTokens: 500000, OutTokens: 400, Model: "claude-opus-4-8", Subagents: 2},
		{ID: "b", TmuxPane: "%2", State: proto.StateWorking, Cwd: "/Users/x/other"},
		// A retained ended session drives the new Resumable panel render path.
		{ID: "c", TmuxPane: "", State: proto.StateEnded, Cwd: "/Users/x/proj", Name: "proj"},
	}
	m.limits = proto.Usage{OK: true, SessionPct: 61, WeeklyPct: 8}
	// A cached backlog so the inline sidebar Issues panel is exercised too.
	m.issueRepoName = "proj"
	// Two ready issues with disjoint Touches so the parallel-safe advisory line
	// renders in the sidebar tracker across all sizes.
	m.issueList = []issues.Issue{
		{Feature: "login", Num: "01", Title: "Add the login form", Status: "ready-for-agent", Touches: []string{"internal/tui/a.go"}},
		{Feature: "login", Num: "02", Title: "Blocked slice", Status: "ready-for-agent", Blocked: true},
		{Feature: "billing", Num: "01", Title: "Stripe webhook handler", Status: "resolved"},
		{Feature: "billing", Num: "03", Title: "Store schema", Status: "ready-for-agent", Touches: []string{"internal/store/db.go"}},
	}
	// Cover the normal dashboard plus the details-peek popup (a distinct render
	// path). Peek also renders a "No session selected" fallback, so exercise an
	// empty session list under modePeek too.
	for _, md := range []mode{modeNormal, modePeek} {
		for _, empty := range []bool{false, true} {
			sessions := m.sessions
			if empty {
				sessions = nil
			}
			for w := 1; w <= 80; w++ {
				for h := 1; h <= 50; h++ {
					func(w, h int, md mode, sessions []proto.Session) {
						defer func() {
							if r := recover(); r != nil {
								t.Fatalf("panic mode=%d empty=%v w=%d h=%d: %v", md, empty, w, h, r)
							}
						}()
						m.sessions = sessions
						m.mode = md
						m.width, m.height = w, h
						m.clampCursor()
						_ = m.View()
					}(w, h, md, sessions)
				}
			}
		}
	}
}

// TestRenderNoPanicIssueViews covers the issue browser and start-work chooser at
// every size — a panic there closes the sidebar's pane, same as any other view.
func TestRenderNoPanicIssueViews(t *testing.T) {
	m := model{issueRepoName: "proj"}
	m.issueList = []issues.Issue{
		{Feature: "login", Num: "01", Title: "Add the login form", Status: "ready-for-agent", Touches: []string{"internal/tui/a.go"}, Path: "/r/login/01.md"},
		{Feature: "login", Num: "02", Title: "Wire up session cookies", Status: "claimed", Path: "/r/login/02.md"},
		{Feature: "billing", Num: "01", Title: "Stripe webhook handler", Status: "resolved", Path: "/r/billing/01.md"},
		{Feature: "billing", Num: "02", Title: "Blocked slice", Status: "ready-for-agent", Blocked: true, Path: "/r/billing/02.md"},
		{Feature: "billing", Num: "03", Title: "Store schema", Status: "ready-for-agent", Touches: []string{"internal/store/db.go"}, Path: "/r/billing/03.md"},
	}
	m.startIssue = m.issueList[0]
	// A multi-select set so the checkbox marker + parallel-dispatch UI render
	// across all sizes (a panic there closes the sidebar's pane).
	m.issueSelected = map[string]bool{
		m.issueList[0].Path: true,
		m.issueList[4].Path: true,
	}
	// Ready-to-merge candidates so the "N ready to merge · m" footer indicator
	// renders across all sizes (Round 2 merge-back).
	m.readyMerges = []mergeCandidate{
		{Path: "/wt/billing-01", Branch: "capataz/billing-01-stripe", Issue: m.issueList[2]},
	}
	// A pending model override so the issues-popup footer override chip and the
	// M picker both render across all sizes.
	m.modelOverride = "opus"
	// cursor 2 = the resolved "billing" issue, exercising the all-resolved footer.
	for _, cursor := range []int{0, 2} {
		for _, empty := range []bool{false, true} {
			list := m.issueList
			if empty {
				list = nil
			}
			for _, md := range []mode{modeIssues, modeStartWork, modeParallelConfirm, modeModelPicker} {
				for w := 1; w <= 80; w++ {
					for h := 1; h <= 50; h++ {
						func(w, h int, md mode) {
							defer func() {
								if r := recover(); r != nil {
									t.Fatalf("panic mode=%d empty=%v cursor=%d w=%d h=%d: %v", md, empty, cursor, w, h, r)
								}
							}()
							m.issueList = list
							m.issueCursor = cursor
							m.mode = md
							m.width, m.height = w, h
							m.clampIssueCursor()
							_ = m.View()
						}(w, h, md)
					}
				}
			}
		}
	}
}
