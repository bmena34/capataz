package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// fakeRunner drives collect() and reportSweep() without a herdr server.
type fakeRunner struct {
	agents    []Agent
	err       error
	plan      string
	planNo    error
	reported  map[string]string // pane id -> last reported token value
	reportErr error
}

func (f *fakeRunner) AgentList() ([]Agent, error) { return f.agents, f.err }
func (f *fakeRunner) PlanUsage() (string, error)  { return f.plan, f.planNo }

func (f *fakeRunner) ReportContext(paneID, value string, _ time.Duration) error {
	if f.reportErr != nil {
		return f.reportErr
	}
	if f.reported == nil {
		f.reported = map[string]string{}
	}
	f.reported[paneID] = value
	return nil
}

func TestCollect_ReadsContextPerPane(t *testing.T) {
	dir := t.TempDir()
	cwd := "/Users/me/code/proj"
	writeTranscript(t, dir, projectSlug(cwd), "sess-1",
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":10,"cache_read_input_tokens":49990}}}`)

	r := &fakeRunner{agents: []Agent{
		{Kind: "claude", SessionID: "sess-1", Cwd: cwd, Status: "working"},
	}}

	rows, err := collect(r, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: got %d, want 1", len(rows))
	}
	if rows[0].Tokens != 50_000 {
		t.Errorf("tokens: got %d, want 50000", rows[0].Tokens)
	}
	if rows[0].Limit != 200_000 {
		t.Errorf("limit: got %d, want 200000", rows[0].Limit)
	}
	if rows[0].Label != "proj" {
		t.Errorf("label: got %q, want %q", rows[0].Label, "proj")
	}
	if rows[0].Note != "" {
		t.Errorf("note: got %q, want empty", rows[0].Note)
	}
}

func TestCollect_AgentListFailurePropagates(t *testing.T) {
	r := &fakeRunner{err: errors.New("herdr server not running")}
	if _, err := collect(r, &limitResolver{}, t.TempDir(), ""); err == nil {
		t.Fatal("expected the agent-list error to propagate")
	}
}

// Panes we can't read context for still appear, with a reason — a pane silently
// missing from the list is more confusing than an annotated one.
func TestCollect_NotesInsteadOfDroppingPanes(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{agents: []Agent{
		{Kind: "codex", SessionID: "x", Cwd: "/repo/a"},
		{Kind: "claude", SessionID: "", Cwd: "/repo/b"},
		{Kind: "claude", SessionID: "ghost", Cwd: "/repo/c"},
	}}

	rows, err := collect(r, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: got %d, want 3", len(rows))
	}
	wants := []string{"codex — no context data", "no session yet", "no transcript"}
	for i, want := range wants {
		if rows[i].Note != want {
			t.Errorf("row %d note: got %q, want %q", i, rows[i].Note, want)
		}
		if rows[i].Tokens != 0 {
			t.Errorf("row %d tokens: got %d, want 0", i, rows[i].Tokens)
		}
	}
}

func TestCollect_TranscriptWithNoTurnsYet(t *testing.T) {
	dir := t.TempDir()
	cwd := "/repo/fresh"
	writeTranscript(t, dir, projectSlug(cwd), "sess-new", `{"type":"user","message":{"role":"user"}}`)

	r := &fakeRunner{agents: []Agent{{Kind: "claude", SessionID: "sess-new", Cwd: cwd}}}
	rows, err := collect(r, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows[0].Note != "no turns yet" {
		t.Errorf("note: got %q, want %q", rows[0].Note, "no turns yet")
	}
}

// The popup is opened with the originating pane's cwd, and opening it with
// --focus can take herdr's `focused` flag off the agent pane — so matching on
// cwd is what reliably marks the pane the user came from.
func TestCollect_MarksOriginatingPaneByCwd(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{agents: []Agent{
		{Kind: "claude", SessionID: "a", Cwd: "/repo/one"},
		{Kind: "claude", SessionID: "b", Cwd: "/repo/two"},
	}}

	rows, err := collect(r, &limitResolver{}, dir, "/repo/two")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// The marked pane sorts first.
	if !rows[0].Marked || rows[0].Label != "two" {
		t.Errorf("expected the originating pane first and marked, got %q marked=%v", rows[0].Label, rows[0].Marked)
	}
	if rows[1].Marked {
		t.Error("non-originating pane should not be marked")
	}
}

func TestCollect_MarksHerdrFocusedPane(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{agents: []Agent{
		{Kind: "claude", SessionID: "a", Cwd: "/repo/one"},
		{Kind: "claude", SessionID: "b", Cwd: "/repo/two", Focused: true},
	}}

	rows, err := collect(r, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rows[0].Marked || rows[0].Label != "two" {
		t.Errorf("expected the focused pane first and marked, got %q marked=%v", rows[0].Label, rows[0].Marked)
	}
}

// A drain fans out worktrees whose basenames can collide; labels must stay
// distinguishable or the user can't tell which gauge is which.
func TestLabelRows_DisambiguatesCollidingBasenames(t *testing.T) {
	rows := []Row{
		{Label: "/Users/me/code/proj"},
		{Label: "/Users/me/worktrees/proj"},
		{Label: "/Users/me/code/other"},
	}
	labelRows(rows)

	want := []string{"code/proj", "worktrees/proj", "other"}
	for i, w := range want {
		if rows[i].Label != w {
			t.Errorf("row %d label: got %q, want %q", i, rows[i].Label, w)
		}
	}
}

// Many panes share a repo (and therefore a dir label); a session the user has
// titled must be labeled by that title — the same terminal_title token herdr's
// sidebar shows — or the gauges are indistinguishable.
func TestLabelRows_PrefersTerminalTitle(t *testing.T) {
	rows := []Row{
		{Label: "/Users/me/code/proj", Title: "auth refactor"},
		{Label: "/Users/me/code/proj"},
	}
	labelRows(rows)

	if rows[0].Label != "auth refactor" {
		t.Errorf("titled row label: got %q, want %q", rows[0].Label, "auth refactor")
	}
	if rows[1].Label != "proj" {
		t.Errorf("untitled row label: got %q, want %q", rows[1].Label, "proj")
	}
}

// Two sessions given the same name still need telling apart.
func TestLabelRows_DisambiguatesCollidingTitles(t *testing.T) {
	rows := []Row{
		{Label: "/Users/me/code/proj", Title: "fix tests"},
		{Label: "/Users/me/worktrees/proj", Title: "fix tests"},
	}
	labelRows(rows)

	want := []string{"fix tests (code/proj)", "fix tests (worktrees/proj)"}
	for i, w := range want {
		if rows[i].Label != w {
			t.Errorf("row %d label: got %q, want %q", i, rows[i].Label, w)
		}
	}
}

// A default pane title (the agent's name, or an echo of the cwd) is not a
// session name — it must fall back to the dir label, not label every pane in
// the repo "claude".
func TestCollect_IgnoresUninformativeTitles(t *testing.T) {
	dir := t.TempDir()
	r := &fakeRunner{agents: []Agent{
		{Kind: "claude", SessionID: "a", Cwd: "/repo/one", Title: "Claude"},
		{Kind: "claude", SessionID: "b", Cwd: "/repo/two", Title: "/repo/two"},
		{Kind: "claude", SessionID: "c", Cwd: "/repo/three", Title: "three"},
		{Kind: "claude", SessionID: "d", Cwd: "/repo/four", Title: "  "},
		{Kind: "claude", SessionID: "e", Cwd: "/repo/five", Title: "usage popup polish"},
	}}

	rows, err := collect(r, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"one", "two", "three", "four", "usage popup polish"}
	for i, w := range want {
		if rows[i].Label != w {
			t.Errorf("row %d label: got %q, want %q", i, rows[i].Label, w)
		}
	}
}

func TestLabelRows_HandlesDegenerateCwds(t *testing.T) {
	rows := []Row{{Label: ""}, {Label: "/"}}
	labelRows(rows)
	for i, r := range rows {
		if strings.TrimSpace(r.Label) == "" {
			t.Errorf("row %d: empty label", i)
		}
	}
}

// Ordering must be stable across refreshes, or rows shuffle under the cursor as
// agents change status.
func TestCollect_OrderIsStableAcrossRefreshes(t *testing.T) {
	dir := t.TempDir()
	agents := []Agent{
		{Kind: "claude", SessionID: "a", Cwd: "/repo/one", Status: "working"},
		{Kind: "claude", SessionID: "b", Cwd: "/repo/two", Focused: true},
		{Kind: "claude", SessionID: "c", Cwd: "/repo/three", Status: "idle"},
	}
	first, err := collect(&fakeRunner{agents: agents}, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	// Same panes, one status changed — ordering must not move.
	agents[0].Status = "blocked"
	second, err := collect(&fakeRunner{agents: agents}, &limitResolver{}, dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range first {
		if first[i].Label != second[i].Label {
			t.Errorf("row %d moved: %q -> %q", i, first[i].Label, second[i].Label)
		}
	}
}
