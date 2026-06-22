// Package hook implements the `capataz hook <kind>` subcommand that Claude Code
// invokes. It reads the hook JSON from stdin, enriches it with the tmux pane,
// and fires it at the daemon. It must be fast and must never block Claude, so
// all errors are swallowed.
package hook

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"time"

	"github.com/bmena34/capataz/internal/logging"
	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/proto"
)

// Run handles one hook invocation. It always returns nil; the caller exits 0.
func Run(kind string) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		logging.Errorf("hook %s: read stdin: %v", kind, err)
		return nil
	}
	var in map[string]any
	_ = json.Unmarshal(data, &in)

	pane := os.Getenv("TMUX_PANE")
	e := proto.Event{
		Kind:           proto.Kind(kind),
		SessionID:      str(in["session_id"]),
		TranscriptPath: str(in["transcript_path"]),
		Cwd:            str(in["cwd"]),
		HookEventName:  str(in["hook_event_name"]),
		PermissionMode: str(in["permission_mode"]),
		ToolName:       str(in["tool_name"]),
		TmuxPane:       pane,
	}

	if pane == "" {
		logging.Infof("hook %s session=%s: TMUX_PANE empty (not in tmux?) — jump/reply won't work",
			kind, short(e.SessionID))
	}

	conn, err := net.DialTimeout("unix", paths.Sock(), 300*time.Millisecond)
	if err != nil {
		logging.Infof("hook %s session=%s: daemon unreachable, dropping event", kind, short(e.SessionID))
		return nil
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(500 * time.Millisecond))
	if err := json.NewEncoder(conn).Encode(proto.Msg{Type: "event", Event: &e}); err != nil {
		logging.Errorf("hook %s: send event: %v", kind, err)
		return nil
	}
	logging.Debugf("hook %s session=%s pane=%q sent", kind, short(e.SessionID), pane)
	return nil
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
