package main

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Row is one rendered line of the context table: a single agent pane.
type Row struct {
	Label  string // short, human-recognizable name for the pane (usually its dir)
	Status string // herdr's agent status, shown dim
	Tokens int    // context tokens in use; 0 when unknown
	Limit  int    // context window to scale against
	Model  string
	Marked bool   // the pane this popup was opened from
	Note   string // why Tokens is unknown ("" when it is known)
}

// View is everything one frame needs. Building it is pure given a Runner, which
// is what makes the renderer testable without a herdr server or a real terminal.
type View struct {
	Plan     string // rendered `capataz usage` output (ANSI included)
	PlanNote string // why Plan is empty, when it is
	ShowPlan bool   // false with -no-plan: render context only
	Rows     []Row
	Err      string // agent-list failure; rows will be empty
	Live     bool   // true when refreshing on a timer (drives the footer)
	Interval time.Duration
}

// collect gathers one frame's worth of state: herdr's agent panes, each mapped
// to its transcript and reduced to a context reading.
//
// selfCwd is the directory this view was opened in — herdr hands the plugin pane
// the focused pane's cwd, so it identifies the pane the user came from. That is
// more reliable than herdr's own `focused` flag here, because opening this popup
// with --focus can itself take focus away from the agent pane.
func collect(r Runner, lim *limitResolver, dir, selfCwd string) ([]Row, error) {
	agents, err := r.AgentList()
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(agents))
	for _, a := range agents {
		row := Row{
			Label:  a.Cwd,
			Status: a.Status,
			Marked: a.Focused || (selfCwd != "" && a.Cwd == selfCwd),
		}
		switch {
		case a.Kind != "" && a.Kind != "claude":
			// Non-Claude agents (codex, …) keep no Claude transcript, so there is
			// nothing to read. Show the pane anyway — an empty row is less
			// confusing than a pane silently missing from the list.
			row.Note = a.Kind + " — no context data"
		case a.SessionID == "":
			row.Note = "no session yet"
		default:
			path := transcriptPath(dir, a.Cwd, a.SessionID)
			if path == "" {
				row.Note = "no transcript"
				break
			}
			t, ok := latestTurn(path)
			if !ok {
				row.Note = "no turns yet"
				break
			}
			row.Tokens = t.contextTokens()
			row.Model = t.model
			row.Limit = lim.limitFor(t.model, row.Tokens)
		}
		rows = append(rows, row)
	}

	labelRows(rows)

	// Marked pane first, then stable by workspace/pane so rows don't shuffle
	// between refreshes as agents change status.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Marked != rows[j].Marked {
			return rows[i].Marked
		}
		return false
	})
	return rows, nil
}

// labelRows turns each row's full cwd (which collect stores in Label) into the
// shortest name that is still unambiguous. The directory basename is the natural
// label, but a drain fans out worktrees whose basenames can collide with each
// other or with the base repo's, so any basename shared by more than one pane
// gets its parent directory prepended.
func labelRows(rows []Row) {
	cwds := make([]string, len(rows))
	counts := map[string]int{}
	for i := range rows {
		cwds[i] = rows[i].Label
		counts[filepath.Base(cwds[i])]++
	}
	for i := range rows {
		base := filepath.Base(cwds[i])
		label := base
		if counts[base] > 1 {
			parent := filepath.Base(filepath.Dir(cwds[i]))
			if parent != "" && parent != "." && parent != string(filepath.Separator) {
				label = parent + "/" + base
			}
		}
		if strings.TrimSpace(label) == "" || label == "." || label == string(filepath.Separator) {
			label = "(unknown)"
		}
		rows[i].Label = label
	}
}
