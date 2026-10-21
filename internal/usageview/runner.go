package usageview

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner is the interface behind every herdr and capataz shell-out this view
// makes. realRunner spawns actual subprocesses; tests supply a fake so the
// refresh/render logic can be exercised without a running herdr server.
//
// Same pattern (and same reason) as cmd/drain's Runner: the usage view is the
// read-only sibling of the orchestrator.
type Runner interface {
	// AgentList runs `herdr agent list` and returns one Agent per agent pane
	// herdr knows about, preserving herdr's ordering.
	AgentList() ([]Agent, error)

	// PlanUsage runs `capataz usage` and returns its rendered gauge text (ANSI
	// color included — it is passed through to the terminal verbatim). This hits
	// an unofficial Anthropic endpoint with the Keychain OAuth token, so the view
	// polls it on a much slower cadence than the context gauges, which are just
	// local file reads.
	PlanUsage() (string, error)

	// ReportContext pushes a pane's context gauge to herdr as the pane-metadata
	// token "context" (rendered by sidebar rows as "$context"), expiring after
	// ttl if not refreshed. Used by -report; the popup never calls it.
	ReportContext(paneID, value string, ttl time.Duration) error
}

// Agent is one herdr-managed agent pane, flattened from `herdr agent list`.
//
// SessionID is the important field: herdr records the agent's own session
// identifier (for Claude, the UUID that names its transcript file), which is
// what lets this view attribute a transcript to a pane exactly rather than
// guessing "newest file in the project dir".
type Agent struct {
	PaneID      string
	WorkspaceID string
	Kind        string // agent program: "claude", "codex", …
	SessionID   string // agent_session.value when kind == "id"; "" when unknown
	Cwd         string
	Title       string
	Status      string // herdr's agent_status: working / blocked / idle / done
	Focused     bool
}

// realRunner executes the actual herdr and capataz subprocesses.
type realRunner struct {
	capatazBin string // default "capataz"; overridden by CAPATAZ_BIN
	herdrBin   string // default "herdr"; overridden by HERDR_BIN
}

// agentListResult is the subset of `herdr agent list` JSON this view needs.
// herdr nests the agents under result.agents (not a flat top-level array), and
// the session identifier is itself nested under agent_session, tagged with a
// "kind" that says how to read "value" — only kind == "id" is a session id we
// can resolve to a transcript.
type agentListResult struct {
	Result struct {
		Agents []struct {
			Agent        string `json:"agent"`
			AgentSession struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			} `json:"agent_session"`
			AgentStatus   string `json:"agent_status"`
			Cwd           string `json:"cwd"`
			ForegroundCwd string `json:"foreground_cwd"`
			Focused       bool   `json:"focused"`
			PaneID        string `json:"pane_id"`
			WorkspaceID   string `json:"workspace_id"`
			Title         string `json:"terminal_title_stripped"`
		} `json:"agents"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) AgentList() ([]Agent, error) {
	out, err := exec.Command(r.herdrBin, "agent", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("herdr agent list: %w", err)
	}
	return parseAgentList(out)
}

// parseAgentList flattens `herdr agent list` output into []Agent.
//
// A session id is only taken when agent_session.kind == "id"; other kinds
// (herdr also reports e.g. path-shaped session handles for some agents) are not
// transcript UUIDs and would resolve to nothing. Panes with no usable session id
// are still returned — the view renders them with a reason instead of silently
// dropping panes the user can see on screen.
func parseAgentList(out []byte) ([]Agent, error) {
	var res agentListResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, fmt.Errorf("herdr agent list: parse output: %w\nraw: %s", err, out)
	}
	if res.Error != nil {
		return nil, fmt.Errorf("herdr agent list: %s: %s", res.Error.Code, res.Error.Message)
	}
	agents := make([]Agent, 0, len(res.Result.Agents))
	for _, a := range res.Result.Agents {
		cwd := a.Cwd
		if cwd == "" {
			cwd = a.ForegroundCwd
		}
		sess := ""
		if a.AgentSession.Kind == "id" {
			sess = a.AgentSession.Value
		}
		agents = append(agents, Agent{
			PaneID:      a.PaneID,
			WorkspaceID: a.WorkspaceID,
			Kind:        a.Agent,
			SessionID:   sess,
			Cwd:         cwd,
			Title:       a.Title,
			Status:      a.AgentStatus,
			Focused:     a.Focused,
		})
	}
	return agents, nil
}

// ReportContext shells out to `herdr pane report-metadata`. The --source id
// scopes the token to this plugin, and --ttl-ms lets a dead reporter's gauges
// expire off the sidebar instead of going stale.
func (r *realRunner) ReportContext(paneID, value string, ttl time.Duration) error {
	out, err := exec.Command(r.herdrBin, "pane", "report-metadata", paneID,
		"--source", metadataSource,
		"--token", "context="+value,
		"--ttl-ms", strconv.Itoa(int(ttl/time.Millisecond)),
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr pane report-metadata %s: %w: %s", paneID, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// PlanUsage shells out to `capataz usage`. capataz is optional for this view —
// the context gauges are read straight from transcripts — so a missing binary is
// reported as a one-line note rather than failing the whole render.
func (r *realRunner) PlanUsage() (string, error) {
	out, err := exec.Command(r.capatazBin, "usage").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("capataz usage: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimRight(string(out), "\n"), nil
}
