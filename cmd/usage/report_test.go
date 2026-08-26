package main

import (
	"errors"
	"testing"
	"time"
)

var errTest = errors.New("test error")

// The reporter feeds the sidebar's "$context" token; only claude panes with a
// readable transcript get one, everything else is left to its TTL.
func TestReportSweep_ReportsReadablePanesOnly(t *testing.T) {
	dir := t.TempDir()
	cwd := "/Users/me/code/proj"
	writeTranscript(t, dir, projectSlug(cwd), "sess-1",
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":10,"cache_read_input_tokens":67990}}}`)

	r := &fakeRunner{agents: []Agent{
		{PaneID: "w1:p1", Kind: "claude", SessionID: "sess-1", Cwd: cwd},
		{PaneID: "w1:p2", Kind: "codex", SessionID: "x", Cwd: cwd},
		{PaneID: "w1:p3", Kind: "claude", SessionID: "", Cwd: cwd},
		{PaneID: "w1:p4", Kind: "claude", SessionID: "ghost", Cwd: cwd},
	}}

	n, err := reportSweep(r, &limitResolver{}, dir, 15*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reported: got %d, want 1", n)
	}
	got, ok := r.reported["w1:p1"]
	if !ok {
		t.Fatalf("pane w1:p1 not reported; reported: %v", r.reported)
	}
	// 68k of 200k = 34%.
	want := "[███░░░░░░░] 34% · 68k"
	if got != want {
		t.Errorf("token value: got %q, want %q", got, want)
	}
	for _, pane := range []string{"w1:p2", "w1:p3", "w1:p4"} {
		if _, ok := r.reported[pane]; ok {
			t.Errorf("pane %s should not have been reported", pane)
		}
	}
}

func TestReportSweep_AgentListFailurePropagates(t *testing.T) {
	r := &fakeRunner{err: errTest}
	if _, err := reportSweep(r, &limitResolver{}, t.TempDir(), time.Second); err == nil {
		t.Fatal("expected the agent-list error to propagate")
	}
}

// A pane that vanishes between the list and the report must not sink the sweep.
func TestReportSweep_SwallowsPerPaneReportErrors(t *testing.T) {
	dir := t.TempDir()
	cwd := "/repo/gone"
	writeTranscript(t, dir, projectSlug(cwd), "s",
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":100}}}`)

	r := &fakeRunner{
		agents:    []Agent{{PaneID: "w1:p9", Kind: "claude", SessionID: "s", Cwd: cwd}},
		reportErr: errTest,
	}
	n, err := reportSweep(r, &limitResolver{}, dir, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("reported: got %d, want 0", n)
	}
}

// The TTL must outlive a single missed sweep but stay short enough that a dead
// reporter's gauges leave the sidebar promptly.
func TestReportTTL(t *testing.T) {
	if got := reportTTL(5 * time.Second); got != 15*time.Second {
		t.Errorf("ttl(5s): got %v, want 15s", got)
	}
	if got := reportTTL(time.Second); got != 10*time.Second {
		t.Errorf("ttl(1s): got %v, want the 10s floor", got)
	}
}
