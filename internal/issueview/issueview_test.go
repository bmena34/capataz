package issueview

import (
	"strings"
	"testing"

	"github.com/bmena34/capataz/internal/issues"
)

func sampleIssueList() []issues.Issue {
	return []issues.Issue{
		{Feature: "alpha", Num: "01", Title: "Add the thing", Status: issues.StatusReadyForAgent, Touches: []string{"a.go"}},
		{Feature: "alpha", Num: "02", Title: "Wire the thing", Status: issues.StatusClaimed, Touches: []string{"b.go"}},
		{Feature: "alpha", Num: "03", Title: "Blocked follow-up", Status: issues.StatusReadyForAgent, BlockedBy: []string{"02"}, Blocked: true},
		{Feature: "beta", Num: "01", Title: "Docs pass", Status: issues.StatusResolved},
	}
}

// TestIssueViewRenderNoPanic is the standalone viewer's safety net: negative
// width/height math (slices, padding) must never panic, matching the dashboard's
// TestRenderNoPanic contract — a render panic closes the pane.
func TestIssueViewRenderNoPanic(t *testing.T) {
	lists := [][]issues.Issue{nil, sampleIssueList()}
	for _, list := range lists {
		for w := 1; w <= 80; w++ {
			for h := 1; h <= 50; h++ {
				m := issueViewModel{repo: "/tmp/x", repoName: "x", list: list, w: w, h: h}
				func() {
					defer func() {
						if r := recover(); r != nil {
							t.Fatalf("View panicked at %dx%d (list=%d): %v", w, h, len(list), r)
						}
					}()
					_ = m.View()
				}()
			}
		}
	}
}

func TestIssueViewShowsBacklog(t *testing.T) {
	m := issueViewModel{repo: "/tmp/x", repoName: "capataz", list: sampleIssueList(), w: 80, h: 24}
	out := m.View()
	for _, want := range []string{"capataz", "alpha/", "beta/", "Add the thing", "blocked"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered view missing %q\n---\n%s", want, out)
		}
	}
}

func TestIssueViewEmptyBacklog(t *testing.T) {
	m := issueViewModel{repo: "/tmp/x", repoName: "empty", list: nil, w: 80, h: 24}
	out := m.View()
	if !strings.Contains(out, "No .scratch backlog") {
		t.Errorf("empty view missing placeholder\n---\n%s", out)
	}
}

// TestAskingBadge: asking has its own label and color, not the unknown-status
// fallback.
func TestAskingBadge(t *testing.T) {
	label, color := issueBadgeParts(issues.StatusAsking)
	if label != "ASKING" {
		t.Errorf("label = %q, want ASKING", label)
	}
	if _, fallback := issueBadgeParts("mid-review"); color == fallback {
		t.Errorf("asking renders with the unknown-status fallback color")
	}
	list := []issues.Issue{{Feature: "alpha", Num: "01", Title: "Q", Status: issues.StatusAsking}}
	m := issueViewModel{repo: "/tmp/x", repoName: "x", list: list, w: 80, h: 24}
	if out := m.View(); !strings.Contains(out, "ASKING") {
		t.Errorf("view missing ASKING badge\n---\n%s", out)
	}
}

// TestIssueViewClamp guards cursor navigation bounds across loads.
func TestIssueViewClamp(t *testing.T) {
	m := issueViewModel{list: sampleIssueList()}
	m.cursor = 99
	m.clamp()
	if m.cursor != len(m.list)-1 {
		t.Fatalf("cursor not clamped to last: got %d", m.cursor)
	}
	m.cursor = -5
	m.clamp()
	if m.cursor != 0 {
		t.Fatalf("cursor not clamped to first: got %d", m.cursor)
	}
	m.list = nil
	m.cursor = 3
	m.clamp()
	if m.cursor != 0 {
		t.Fatalf("empty-list cursor not reset: got %d", m.cursor)
	}
}
