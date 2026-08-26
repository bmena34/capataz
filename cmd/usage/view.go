package main

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Row is one rendered line of the context table: a single agent pane.
type Row struct {
	Label  string // short, human-recognizable name for the pane (title or dir)
	Title  string // pane terminal title, when it actually names the session
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
			Title:  usableTitle(a),
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

// labelRows picks each row's display label: the pane's terminal title when one
// is set (the same token herdr's sidebar shows as terminal_title), because many
// panes in one repo share a directory name and a user-named session is the only
// label that tells them apart. Untitled panes fall back to the cwd-derived
// label: the directory basename, parent-prefixed when basenames collide (a
// drain fans out worktrees whose basenames can match each other or the base
// repo's). Titled panes whose titles also collide get the dir label appended so
// two sessions named the same are still distinguishable.
func labelRows(rows []Row) {
	cwds := make([]string, len(rows))
	titleCounts := map[string]int{}
	for i := range rows {
		cwds[i] = rows[i].Label
		if rows[i].Title != "" {
			titleCounts[rows[i].Title]++
		}
	}
	// Basename collisions only matter among rows whose final label will show a
	// dir label — untitled rows plus title-collision groups. A uniquely-titled
	// pane never displays its dir, so it shouldn't force a parent prefix onto
	// the others.
	baseCounts := map[string]int{}
	for i := range rows {
		if rows[i].Title == "" || titleCounts[rows[i].Title] > 1 {
			baseCounts[filepath.Base(cwds[i])]++
		}
	}
	for i := range rows {
		base := filepath.Base(cwds[i])
		dirLabel := base
		if baseCounts[base] > 1 {
			parent := filepath.Base(filepath.Dir(cwds[i]))
			if parent != "" && parent != "." && parent != string(filepath.Separator) {
				dirLabel = parent + "/" + base
			}
		}
		if strings.TrimSpace(dirLabel) == "" || dirLabel == "." || dirLabel == string(filepath.Separator) {
			dirLabel = "(unknown)"
		}
		switch {
		case rows[i].Title == "":
			rows[i].Label = dirLabel
		case titleCounts[rows[i].Title] > 1:
			rows[i].Label = rows[i].Title + " (" + dirLabel + ")"
		default:
			rows[i].Label = rows[i].Title
		}
	}
}

// usableTitle returns the pane's terminal title when it actually names the
// session, and "" when it is just the noise an unnamed pane carries — empty,
// the agent program's name ("claude"), or an echo of the cwd — which would
// label every pane in a repo identically, the exact problem titles solve.
func usableTitle(a Agent) string {
	t := strings.TrimSpace(a.Title)
	switch {
	case t == "", strings.EqualFold(t, a.Kind), t == a.Cwd, t == filepath.Base(a.Cwd):
		return ""
	}
	return t
}
