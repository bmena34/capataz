// Package proto defines the wire types shared between the hook, the daemon,
// and the TUI. Messages are newline-delimited JSON over a Unix socket.
package proto

// Kind is the category of event a hook reports. It maps directly to the
// Claude Code hook event that produced it.
type Kind string

const (
	KindPermission   Kind = "permission_prompt" // Notification: needs approval
	KindIdle         Kind = "idle_prompt"        // Notification: idle, waiting for you
	KindStop         Kind = "stop"               // Stop: finished a turn (done or asking)
	KindStopFailure  Kind = "stopfailure"        // StopFailure: turn ended on API error
	KindSessionStart Kind = "session_start"      // SessionStart
	KindSessionEnd   Kind = "session_end"        // SessionEnd
	KindWorking      Kind = "working"            // UserPromptSubmit / PreToolUse heartbeat
	KindSubagentStop Kind = "subagent_stop"      // SubagentStop: a Task subagent finished
)

// State is the derived, user-facing status of a session.
type State string

const (
	StateWorking         State = "working"
	StateNeedsPermission State = "needs_permission"
	StateWaiting         State = "waiting" // idle, waiting for next prompt
	StateAsking          State = "asking"  // ended turn on a question
	StateDone            State = "done"    // ended turn, no question
	StateError           State = "error"
	StateEnded           State = "ended"
)

// Priority orders states so the TUI can surface "needs you" rows first.
// Lower number = higher up the list.
func (s State) Priority() int {
	switch s {
	case StateNeedsPermission:
		return 0
	case StateWaiting:
		return 1
	case StateAsking:
		return 2
	case StateError:
		return 3
	case StateWorking:
		return 4
	case StateDone:
		return 5
	default:
		return 6
	}
}

// NeedsYou reports whether a session is blocked on the user.
func (s State) NeedsYou() bool {
	switch s {
	case StateNeedsPermission, StateWaiting, StateAsking, StateError:
		return true
	default:
		return false
	}
}

// Event is what a hook sends to the daemon.
type Event struct {
	Kind           Kind   `json:"kind"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	TmuxPane       string `json:"tmux_pane"`
	HookEventName  string `json:"hook_event_name"`
	PermissionMode string `json:"permission_mode"`
	ToolName       string `json:"tool_name"` // PreToolUse: the tool being called ("Agent"/"Task" = subagent spawn)
}

// Session is the daemon's tracked state for one Claude Code session.
type Session struct {
	ID             string `json:"id"`
	Cwd            string `json:"cwd"`
	TmuxPane       string `json:"tmux_pane"`
	TranscriptPath string `json:"transcript_path"`
	State      State  `json:"state"`
	LastEvent  string `json:"last_event"`
	LastChange int64  `json:"last_change"` // unix nanos
	Started    int64  `json:"started"`     // unix nanos
	Summary    string `json:"summary"`     // last assistant snippet

	// Token usage from the latest assistant turn.
	ContextTokens int    `json:"context_tokens"` // input + cache_read + cache_creation
	OutTokens     int    `json:"out_tokens"`     // output tokens of the last turn
	Model         string `json:"model"`

	// Subagents is the number of Task subagents currently running under this
	// session (Task spawns ++ / SubagentStop --, reset when the turn ends).
	Subagents int `json:"subagents"`

	// StateSince is when the session entered its current state (unix nanos).
	StateSince int64 `json:"state_since"`
	// CostUSD is the cumulative estimated cost of the session so far.
	CostUSD float64 `json:"cost_usd"`
	// Git context for the session's cwd. GitDirty is -1 when unknown/not a repo.
	GitBranch string `json:"git_branch"`
	GitDirty  int    `json:"git_dirty"`
}

// Msg is a client->daemon envelope (hook events and TUI subscribe requests).
type Msg struct {
	Type  string `json:"type"` // "event" | "subscribe"
	Event *Event `json:"event,omitempty"`
}

// Usage is the account's live plan usage (the Settings → Usage numbers). The
// daemon polls it once and pushes it to every dashboard, so N sidebars don't
// each hit the API. Reset times are unix nanos (0 = unknown).
type Usage struct {
	SessionPct   int   `json:"session_pct"`   // 5-hour "current session" utilization
	SessionReset int64 `json:"session_reset"` // unix nanos
	WeeklyPct    int   `json:"weekly_pct"`    // 7-day "all models" utilization
	WeeklyReset  int64 `json:"weekly_reset"`  // unix nanos
	OK           bool  `json:"ok"`
}

// Push is a daemon->TUI message.
type Push struct {
	Type     string    `json:"type"` // "snapshot" | "update" | "remove" | "usage"
	Sessions []Session `json:"sessions,omitempty"`
	Session  *Session  `json:"session,omitempty"`
	Usage    *Usage    `json:"usage,omitempty"`
}
