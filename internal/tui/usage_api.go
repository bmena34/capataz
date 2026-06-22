package tui

import (
	"fmt"
	"time"
)

// Usage is now fetched centrally by the daemon and delivered over the push
// stream as proto.Usage (see internal/daemon/usage.go). These are the
// display-only helpers the dashboard uses to render it.

// nanoTime converts unix nanos to a time.Time, returning the zero value for 0
// (unknown) so resetStr renders it as empty.
func nanoTime(n int64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// resetStr formats a reset time: relative ("in 4h30m") when under a day,
// otherwise an absolute day+time ("Wed 3:59pm").
func resetStr(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Until(t)
	if d <= 0 {
		return "resetting…"
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("in %dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return t.Format("Mon 3:04pm")
}
