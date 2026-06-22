package tui

import (
	"testing"

	"github.com/bmena34/capataz/internal/proto"
)

func TestRenderNoPanic(t *testing.T) {
	m := model{}
	m.sessions = []proto.Session{
		{ID: "a", TmuxPane: "%1", State: proto.StateWaiting, Cwd: "/Users/x/proj", ContextTokens: 500000, OutTokens: 400, Model: "claude-opus-4-8", Subagents: 2},
		{ID: "b", TmuxPane: "%2", State: proto.StateWorking, Cwd: "/Users/x/other"},
	}
	m.limits = proto.Usage{OK: true, SessionPct: 61, WeeklyPct: 8}
	for w := 1; w <= 80; w++ {
		for h := 1; h <= 50; h++ {
			func(w, h int) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic at w=%d h=%d: %v", w, h, r)
					}
				}()
				m.width, m.height = w, h
				_ = m.View()
			}(w, h)
		}
	}
}
