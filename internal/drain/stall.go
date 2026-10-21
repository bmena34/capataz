package drain

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/bmena34/capataz/internal/issues"
)

// stallPoll is how often a working agent's worktree is checked for changes.
const stallPoll = 60 * time.Second

// Bounds on one newestMtime walk, so a huge worktree can't stall the drain.
const (
	walkMaxFiles = 50_000
	walkMaxTime  = 5 * time.Second
)

// walkSkip names directories newestMtime never descends into.
var walkSkip = map[string]bool{".git": true, "node_modules": true, "vendor": true, ".terraform": true}

var errWalkCap = errors.New("walk cap reached")

// newestMtime returns the newest modification time under root, skipping
// walkSkip directories and stopping early at walkMaxFiles or walkMaxTime.
func newestMtime(root string) (time.Time, error) {
	var newest time.Time
	visited := 0
	deadline := time.Now().Add(walkMaxTime)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip it
		}
		if d.IsDir() && path != root && walkSkip[d.Name()] {
			return filepath.SkipDir
		}
		visited++
		if visited > walkMaxFiles || time.Now().After(deadline) {
			return errWalkCap
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if err != nil && !errors.Is(err, errWalkCap) {
		return newest, err
	}
	return newest, nil
}

// stallWatch notifies the operator once when a working agent's worktree goes
// stallAfter without a file change. A later change re-arms it. It only
// notifies — it never nudges or stops the agent.
type stallWatch struct {
	cfg      Config
	runner   Runner
	iss      WaveIssue
	agent    string
	paneID   string
	worktree string
	after    time.Duration

	newest  time.Time // newest mtime seen in the worktree
	since   time.Time // when the stall clock last restarted
	alerted bool
}

func newStallWatch(cfg Config, runner Runner, iss WaveIssue, agent, paneID, worktree string) *stallWatch {
	minutes := cfg.StallMinutes
	if minutes <= 0 {
		minutes = 15
	}
	w := &stallWatch{cfg: cfg, runner: runner, iss: iss, agent: agent, paneID: paneID, worktree: worktree,
		after: time.Duration(minutes) * time.Minute}
	w.newest, _ = cfg.walk(worktree)
	w.since = cfg.now()
	return w
}

// rearm restarts the stall clock, so time the agent spent blocked or asking
// never counts toward a stall.
func (w *stallWatch) rearm() { w.since = w.cfg.now() }

// check runs one poll: a newer mtime restarts the clock and re-arms the alert;
// otherwise, past the threshold and not yet alerted, it notifies once — unless
// the issue is asking, which restarts the clock instead.
func (w *stallWatch) check(logf func(string, ...any)) {
	now := w.cfg.now()
	m, err := w.cfg.walk(w.worktree)
	if err != nil {
		logf("drain: [%s] cannot scan worktree %s for changes: %v", w.iss.Number, w.worktree, err)
		return
	}
	if m.After(w.newest) {
		w.newest, w.since, w.alerted = m, now, false
		return
	}
	if w.alerted || now.Sub(w.since) < w.after {
		return
	}
	if st, err := w.runner.IssueStatus(w.cfg.RepoPath, w.iss.Path); err == nil && normStatus(st) == issues.StatusAsking {
		w.since = now
		return
	}
	w.alerted = true
	mins := int(now.Sub(w.since).Minutes())
	logf("drain: [%s] STALLED — agent %s (pane %s) hasn't changed anything in %d min (worktree %s)", w.iss.Number, w.agent, w.paneID, mins, w.worktree)
	title := fmt.Sprintf("capataz: %s %s hasn't changed anything in %d min", w.cfg.Feature, w.iss.Number, mins)
	if err := w.runner.Notify(title, fmt.Sprintf("Agent %s may be stuck — check pane %s (worktree %s).", w.agent, w.paneID, w.worktree)); err != nil {
		logf("drain: [%s] notification failed: %v", w.iss.Number, err)
	}
}
