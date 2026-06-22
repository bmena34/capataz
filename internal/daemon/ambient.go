// Ambient awareness: the daemon publishes a "needs you" count into a tmux
// status-line badge so it's visible out-of-band. (Desktop notifications were
// removed — the per-session dashboard sidebar is the awareness signal now.)
package daemon

import (
	"fmt"
	"os/exec"

	"github.com/bmena34/capataz/internal/proto"
)

// needsSummaryLocked counts sessions blocked on the user and picks an urgency
// color (red for permission/error, amber otherwise). Caller holds h.mu (or runs
// before the daemon accepts connections, e.g. startup reload).
func (h *hub) needsSummaryLocked() (count int, color string) {
	color = "colour214" // amber
	for _, s := range h.sessions {
		if s.State.NeedsYou() {
			count++
			if s.State == proto.StateNeedsPermission || s.State == proto.StateError {
				color = "colour196" // red — more urgent
			}
		}
	}
	return count, color
}

// updateBadge publishes the needs-you count into the tmux user option
// @capataz_needs (empty when all clear) and asks tmux to redraw. Non-destructive:
// it only sets a private user option — `capataz statusline` opts it into the
// status line. No-op when tmux isn't reachable.
func (h *hub) updateBadge(count int, color string) {
	val := ""
	if count > 0 {
		val = fmt.Sprintf("#[fg=%s,bold]⚑ %d#[default]", color, count)
	}
	if err := exec.Command("tmux", "set-option", "-g", "@capataz_needs", val).Run(); err != nil {
		return // tmux not running
	}
	_ = exec.Command("tmux", "refresh-client", "-S").Run()
}
