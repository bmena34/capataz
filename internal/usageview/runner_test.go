package usageview

import "testing"

// ---------------------------------------------------------------------------
// herdr agent list parsing
// ---------------------------------------------------------------------------

// realAgentList is a verbatim `herdr agent list` response (herdr 0.8.x), kept as
// a fixture so a change in herdr's shape surfaces here rather than in a popup
// that silently lists no panes.
const realAgentList = `{"id":"cli:agent:list","result":{"agents":[{"agent":"claude","agent_session":{"agent":"claude","kind":"id","source":"herdr:claude","value":"592d81a4-7344-4f13-82f0-00ce33951dd5"},"agent_status":"blocked","cwd":"/Users/me/code/capataz-herdr","focused":true,"foreground_cwd":"/Users/me/code/capataz-herdr","pane_id":"w2:p1","revision":2,"state_change_seq":23,"tab_id":"w2:t1","terminal_id":"term_65892e713bc9a1","terminal_title":"⠐ Add context display","terminal_title_stripped":"Add context display","workspace_id":"w2"}],"type":"agent_list"}}`

func TestParseAgentList_Real(t *testing.T) {
	agents, err := parseAgentList([]byte(realAgentList))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents: got %d, want 1", len(agents))
	}
	a := agents[0]
	if a.SessionID != "592d81a4-7344-4f13-82f0-00ce33951dd5" {
		t.Errorf("session id: got %q", a.SessionID)
	}
	if a.Cwd != "/Users/me/code/capataz-herdr" {
		t.Errorf("cwd: got %q", a.Cwd)
	}
	if a.Kind != "claude" || a.Status != "blocked" || !a.Focused {
		t.Errorf("kind/status/focused: got %q/%q/%v", a.Kind, a.Status, a.Focused)
	}
	if a.PaneID != "w2:p1" || a.WorkspaceID != "w2" {
		t.Errorf("pane/workspace: got %q/%q", a.PaneID, a.WorkspaceID)
	}
	if a.Title != "Add context display" {
		t.Errorf("title: got %q", a.Title)
	}
}

func TestParseAgentList_NonIDSessionKindIsDropped(t *testing.T) {
	// Only kind == "id" carries a transcript UUID. Anything else must not be
	// mistaken for one, or we would glob for a file that cannot exist.
	raw := `{"result":{"agents":[
		{"agent":"claude","agent_session":{"kind":"path","value":"/tmp/whatever"},"cwd":"/repo","pane_id":"p1"}
	]}}`
	agents, err := parseAgentList([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("agents: got %d, want 1", len(agents))
	}
	if agents[0].SessionID != "" {
		t.Errorf("session id: got %q, want empty for kind=path", agents[0].SessionID)
	}
}

func TestParseAgentList_FallsBackToForegroundCwd(t *testing.T) {
	raw := `{"result":{"agents":[
		{"agent":"claude","agent_session":{"kind":"id","value":"abc"},"cwd":"","foreground_cwd":"/repo/sub","pane_id":"p1"}
	]}}`
	agents, err := parseAgentList([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agents[0].Cwd != "/repo/sub" {
		t.Errorf("cwd: got %q, want /repo/sub", agents[0].Cwd)
	}
}

func TestParseAgentList_NoAgents(t *testing.T) {
	agents, err := parseAgentList([]byte(`{"result":{"agents":[],"type":"agent_list"}}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("agents: got %d, want 0", len(agents))
	}
}

func TestParseAgentList_ErrorObject(t *testing.T) {
	raw := `{"error":{"code":"no_server","message":"herdr server not running"}}`
	if _, err := parseAgentList([]byte(raw)); err == nil {
		t.Fatal("expected an error for an error-shaped response")
	}
}

func TestParseAgentList_Garbage(t *testing.T) {
	if _, err := parseAgentList([]byte("not json")); err == nil {
		t.Fatal("expected a parse error")
	}
}
