package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Runner is the interface behind all capataz and herdr shell-outs.
// In production, realRunner executes actual subprocesses. In tests, a
// fake implementation drives the loop without spawning anything.
type Runner interface {
	// Waves runs `capataz issue waves <feature> --json` and returns the raw JSON.
	// repo is the directory the command runs in — `capataz issue waves` resolves
	// the backlog from its cwd (the primary checkout of that dir), so this must be
	// the target repo, not the drain process's own cwd.
	Waves(feature, repo string) ([]byte, error)

	// Claim runs `capataz issue claim <path>`.
	Claim(issuePath string) error

	// Resolve runs `capataz issue resolve <path>`.
	Resolve(issuePath string) error

	// WorktreeCreate runs `herdr worktree create --no-focus --branch <branch> --cwd <cwd>`
	// and returns the workspace_id from the JSON result.
	WorktreeCreate(branch, cwd string) (workspaceID string, err error)

	// PaneList runs `herdr pane list --workspace <workspaceID>` and returns the
	// first pane_id found in that workspace.
	PaneList(workspaceID string) (paneID string, err error)

	// AgentStart runs `herdr agent start <name> --kind claude --pane <paneID> -- <prompt>`.
	AgentStart(name, paneID, prompt string, skipPerms bool) error

	// AgentWait runs `herdr agent wait <target> --until done --until blocked --timeout <ms>`.
	// Returns the final status string ("done", "blocked", "timeout", or error text).
	AgentWait(target string, timeoutMS int) (status string, err error)
}

// realRunner executes the actual capataz and herdr subprocesses.
type realRunner struct {
	capatazBin string // default "capataz"; overridden by CAPATAZ_BIN
	herdrBin   string // default "herdr"; overridden by HERDR_BIN
}

func (r *realRunner) Waves(feature, repo string) ([]byte, error) {
	cmd := exec.Command(r.capatazBin, "issue", "waves", feature, "--json")
	cmd.Dir = repo // waves resolves the backlog from cwd; root it in the target repo
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("capataz issue waves: %w", err)
	}
	return out, nil
}

func (r *realRunner) Claim(issuePath string) error {
	out, err := exec.Command(r.capatazBin, "issue", "claim", issuePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("capataz issue claim %s: %w\n%s", issuePath, err, out)
	}
	return nil
}

func (r *realRunner) Resolve(issuePath string) error {
	out, err := exec.Command(r.capatazBin, "issue", "resolve", issuePath).CombinedOutput()
	if err != nil {
		return fmt.Errorf("capataz issue resolve %s: %w\n%s", issuePath, err, out)
	}
	return nil
}

// worktreeCreateResult is the subset of herdr worktree create's JSON output
// that we need: specifically the workspace_id of the newly created workspace.
type worktreeCreateResult struct {
	Result struct {
		WorkspaceID string `json:"workspace_id"`
		// herdr may also return an open_workspace_id when it opens an existing one
		OpenWorkspaceID string `json:"open_workspace_id"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) WorktreeCreate(branch, cwd string) (string, error) {
	args := []string{"worktree", "create", "--no-focus", "--branch", branch, "--cwd", cwd}
	out, err := exec.Command(r.herdrBin, args...).Output()
	if err != nil {
		return "", fmt.Errorf("herdr worktree create --branch %s: %w\noutput: %s", branch, err, out)
	}
	var result worktreeCreateResult
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("herdr worktree create: parse output: %w\nraw: %s", err, out)
	}
	if result.Error != nil {
		return "", fmt.Errorf("herdr worktree create: %s: %s", result.Error.Code, result.Error.Message)
	}
	wsID := result.Result.WorkspaceID
	if wsID == "" {
		wsID = result.Result.OpenWorkspaceID
	}
	if wsID == "" {
		return "", fmt.Errorf("herdr worktree create: no workspace_id in output: %s", out)
	}
	return wsID, nil
}

// paneListResult is the subset of herdr pane list's JSON output we need.
type paneListResult struct {
	Result struct {
		Panes []struct {
			PaneID string `json:"pane_id"`
		} `json:"panes"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) PaneList(workspaceID string) (string, error) {
	out, err := exec.Command(r.herdrBin, "pane", "list", "--workspace", workspaceID).Output()
	if err != nil {
		return "", fmt.Errorf("herdr pane list --workspace %s: %w\noutput: %s", workspaceID, err, out)
	}
	var result paneListResult
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("herdr pane list: parse output: %w\nraw: %s", err, out)
	}
	if result.Error != nil {
		return "", fmt.Errorf("herdr pane list: %s: %s", result.Error.Code, result.Error.Message)
	}
	if len(result.Result.Panes) == 0 {
		return "", fmt.Errorf("herdr pane list --workspace %s: no panes found", workspaceID)
	}
	paneID := result.Result.Panes[0].PaneID
	if paneID == "" {
		return "", fmt.Errorf("herdr pane list --workspace %s: empty pane_id", workspaceID)
	}
	return paneID, nil
}

func (r *realRunner) AgentStart(name, paneID, prompt string, skipPerms bool) error {
	args := []string{"agent", "start", name, "--kind", "claude", "--pane", paneID}
	if skipPerms {
		// Pass --dangerously-skip-permissions as an agent arg after --
		args = append(args, "--", "--dangerously-skip-permissions", prompt)
	} else {
		args = append(args, "--", prompt)
	}
	out, err := exec.Command(r.herdrBin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr agent start %s: %w\n%s", name, err, out)
	}
	return nil
}

// agentWaitResult parses the subset of herdr agent wait output we need.
type agentWaitResult struct {
	Result *struct {
		Status string `json:"status"`
		State  string `json:"state"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) AgentWait(target string, timeoutMS int) (string, error) {
	args := []string{
		"agent", "wait", target,
		"--until", "done",
		"--until", "blocked",
		"--timeout", fmt.Sprintf("%d", timeoutMS),
	}
	out, err := exec.Command(r.herdrBin, args...).Output()
	if err != nil {
		// A timeout from herdr returns non-zero — treat it as "timeout" status
		// rather than a hard error, so the loop can surface it and continue.
		combined, _ := exec.Command(r.herdrBin, args...).CombinedOutput()
		if strings.Contains(string(combined), "timeout") || strings.Contains(err.Error(), "exit status") {
			return "timeout", nil
		}
		return "error", fmt.Errorf("herdr agent wait %s: %w\noutput: %s", target, err, out)
	}
	var result agentWaitResult
	if jsonErr := json.Unmarshal(out, &result); jsonErr != nil {
		// Non-JSON or partial output — treat as done if command succeeded
		return "done", nil
	}
	if result.Error != nil {
		// A timeout error from herdr
		if strings.Contains(result.Error.Code, "timeout") {
			return "timeout", nil
		}
		return "error", fmt.Errorf("herdr agent wait %s: %s: %s", target, result.Error.Code, result.Error.Message)
	}
	if result.Result != nil {
		if result.Result.Status != "" {
			return result.Result.Status, nil
		}
		if result.Result.State != "" {
			return result.Result.State, nil
		}
	}
	// Command succeeded; assume done.
	return "done", nil
}
