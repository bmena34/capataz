package usageview

import (
	"fmt"
	"io"
	"time"
)

// The reporter is the sidebar-facing sibling of the popup: instead of drawing
// gauges in its own pane, it pushes each agent pane's context reading back into
// herdr as a pane-metadata token, which sidebar agent rows render via the
// "$context" token (herdr: "Custom values reported through pane metadata use a
// $name token"). Same transcript arithmetic as the popup, different sink.

// metadataSource identifies this plugin to `herdr pane report-metadata`. herdr
// scopes reported metadata by source so unrelated reporters don't clobber each
// other's tokens.
const metadataSource = "bmena34.capataz"

// maxReportFailures is how many consecutive agent-list failures the reporter
// tolerates before exiting. Sweeps only fail when herdr itself is unreachable —
// per-pane report errors don't count — so a run of them means the server is
// gone and the daemon should die with it; the next server start re-launches it
// via the plugin's startup hook.
const maxReportFailures = 12

// reportTTL is how long a pushed token stays valid without a refresh. Three
// sweep intervals: one missed sweep keeps the gauge, a dead reporter expires it
// off the sidebar instead of leaving a stale reading.
func reportTTL(interval time.Duration) time.Duration {
	ttl := 3 * interval
	if ttl < 10*time.Second {
		ttl = 10 * time.Second
	}
	return ttl
}

// contextToken renders the compact sidebar gauge: "[████░░░░░░] 34% · 349k".
// Plain text, no ANSI — sidebar styling belongs to the user's row config — and
// sized for herdr's default sidebar width (26 cells).
func contextToken(tokens, limit int) string {
	p := painter{on: false}
	pct := pctOf(tokens, limit)
	return fmt.Sprintf("%s %d%% · %s", p.bar(pct, 10), pct, fmtTokens(tokens))
}

// reportSweep pushes one round of context tokens: every claude pane with a
// readable transcript gets a gauge. Panes we can't read (other agent kinds, no
// session yet) are simply skipped — their old token, if any, ages out via the
// TTL, which is the sidebar equivalent of the popup's per-row notes.
//
// It returns how many panes were reported. Per-pane report failures are
// swallowed (a pane can close between the list and the report); only a failed
// agent list is an error, since it means herdr itself didn't answer.
func reportSweep(r Runner, lim *limitResolver, dir string, ttl time.Duration) (int, error) {
	agents, err := r.AgentList()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, a := range agents {
		if (a.Kind != "" && a.Kind != "claude") || a.SessionID == "" {
			continue
		}
		path := transcriptPath(dir, a.Cwd, a.SessionID)
		if path == "" {
			continue
		}
		t, ok := latestTurn(path)
		if !ok {
			continue
		}
		tokens := t.contextTokens()
		val := contextToken(tokens, lim.limitFor(t.model, tokens))
		if err := r.ReportContext(a.PaneID, val, ttl); err != nil {
			continue
		}
		n++
	}
	return n, nil
}

// runReport is the daemon loop behind -report: sweep, sleep, repeat, until
// herdr stops answering for maxReportFailures sweeps in a row.
func runReport(r Runner, lim *limitResolver, dir string, interval time.Duration, log io.Writer) error {
	ttl := reportTTL(interval)
	fails := 0
	for {
		if _, err := reportSweep(r, lim, dir, ttl); err != nil {
			fails++
			if fails >= maxReportFailures {
				return fmt.Errorf("herdr unreachable for %d sweeps, exiting: %w", fails, err)
			}
			fmt.Fprintf(log, "sweep failed (%d/%d): %v\n", fails, maxReportFailures, err)
		} else {
			fails = 0
		}
		time.Sleep(interval)
	}
}
