package drain

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bmena34/capataz/internal/issues"
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

	// CommitsAhead returns how many commits <branch> is ahead of the base repo's
	// HEAD (git -C repo rev-list --count HEAD..branch). Zero means the dispatched
	// agent produced no work — the drain treats that as a failed issue, not a
	// clean merge. The drain deliberately does NOT claim or resolve: capataz-work
	// owns claim → implement → resolve and refuses a pre-claimed issue, so the
	// drain only verifies the branch has commits, then merges.
	CommitsAhead(repo, branch string) (int, error)

	// WorktreeCreate runs `herdr worktree create --no-focus --branch <branch> --cwd <cwd>`
	// and returns the new workspace_id and the worktree's on-disk checkout path
	// (needed to pre-seed folder-trust before starting the agent).
	WorktreeCreate(branch, cwd string) (workspaceID, checkoutPath string, err error)

	// TrustDir pre-seeds Claude folder-trust for a worktree dir (capataz trust-dir).
	TrustDir(path string) error

	// WorktreeDirty reports whether the worktree at path has uncommitted or
	// untracked changes (git status --porcelain non-empty). The drain checks it
	// before any cleanup so unlanded work is never deleted.
	WorktreeDirty(path string) (bool, error)

	// IssueStatus returns the issue file's current Status: (lowercased), read
	// straight from the file at path. The drain uses it to tell "agent stopped to
	// ask a question" (asking, needs-info, ready-for-human) from "agent did nothing".
	IssueStatus(path string) (string, error)

	// IssueQuestion / IssueAnswer return the latest agent question / operator
	// answer recorded on the issue file (capataz issue ask / answer), or "".
	// IssueAnswer also returns how many answers the file holds, so the drain can
	// tell an answer it has not delivered yet from one it has.
	IssueQuestion(path string) (string, error)
	IssueAnswer(path string) (string, int, error)

	// IssueExchanges returns every operator answer on the issue file, each with
	// the question(s) it answers, in order — so the drain can deliver several
	// answers that piled up while the agent was still in its turn.
	IssueExchanges(path string) ([]issues.Exchange, error)

	// Notify raises an operator notification (`herdr notification show`).
	Notify(title, body string) error

	// DeleteBranch deletes <branch> in repo — cleanup after a failed dispatch.
	DeleteBranch(repo, branch string) error

	// OpenBaseWorkspace runs `herdr workspace create --no-focus --cwd <cwd>` and
	// returns the new workspace_id — used to get a pane in the BASE repo so a
	// fix-conflict agent can run there. (`herdr worktree open` is the wrong verb:
	// it requires --path/--branch and is for linked worktrees, not the primary
	// checkout.)
	OpenBaseWorkspace(cwd string) (workspaceID string, err error)

	// PaneList runs `herdr pane list --workspace <workspaceID>` and returns the
	// first pane_id found in that workspace.
	PaneList(workspaceID string) (paneID string, err error)

	// AgentStart launches claude in a pane with the permission posture (default:
	// --allowedTools; skipPerms: --dangerously-skip-permissions) but NO prompt.
	AgentStart(name, paneID string, skipPerms bool) error

	// AgentPrompt submits the task prompt to an already-started agent.
	AgentPrompt(name, prompt string) error

	// AgentReachedWorking reports whether the agent left idle (working/done/
	// blocked) shortly after a prompt — i.e. it received the task. False → the
	// prompt was likely dropped and should be resubmitted. Only meaningful for
	// a fresh (idle) agent: one that already ended a turn is still done.
	AgentReachedWorking(name string) bool

	// AgentTurn reads the agent's current state from `herdr agent list`: its
	// status and completion_seq (bumped each time it ends a turn). Returns
	// statusGone when herdr no longer lists the agent.
	AgentTurn(name string) (status string, completionSeq int, err error)

	// MergeResolved reports whether repo has no in-progress conflicted merge.
	MergeResolved(repo string) (bool, error)

	// ClosePane closes a herdr pane (tears down a fix-conflict base workspace).
	ClosePane(paneID string) error

	// AgentWait runs `herdr agent wait <target> --until done --until idle --until blocked --timeout <ms>`.
	// Returns "done", "blocked", "timeout", or statusGone when herdr no longer
	// knows the agent (pane closed, agent crashed).
	AgentWait(target string, timeoutMS int) (status string, err error)

	// AgentUnblocked waits for a blocked agent to leave blocked:
	// `herdr agent wait <target> --until working --until done --until idle --timeout <ms>`.
	// Returns "working", "done", "timeout", or statusGone.
	AgentUnblocked(target string, timeoutMS int) (status string, err error)

	// AgentResumed waits for an agent that ended its turn to start another one
	// on its own (Claude Code wakes it when a background shell finishes):
	// `herdr agent wait <target> --until working --until blocked --timeout <ms>`.
	// Returns "working", "blocked", "timeout", or statusGone.
	AgentResumed(target string, timeoutMS int) (status string, err error)

	// Merge runs `git -C <repo> merge --no-ff <branch>`.
	// Returns (false, nil) on a clean merge. Returns (true, nil) when the merge
	// left conflicted files in the working tree (mid-merge state, ready for a
	// fix-conflict agent). Returns (false, err) for all other git failures.
	Merge(branch, repo string) (conflicted bool, err error)

	// WorktreeRemove runs `herdr worktree remove --workspace <workspaceID>`.
	// herdr owns the worktree lifecycle, so we use its remove verb rather than
	// git worktree remove directly.
	WorktreeRemove(workspaceID string) error
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

func (r *realRunner) CommitsAhead(repo, branch string) (int, error) {
	out, err := exec.Command("git", "-C", repo, "rev-list", "--count", "HEAD.."+branch).Output()
	if err != nil {
		return 0, fmt.Errorf("git -C %s rev-list --count HEAD..%s: %w", repo, branch, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse commit count %q: %w", strings.TrimSpace(string(out)), err)
	}
	return n, nil
}

// worktreeCreateResult is the subset of herdr worktree create/open JSON output
// that we need. herdr nests the workspace_id — it is NOT a flat result.workspace_id.
// create returns it under result.workspace.workspace_id (and result.root_pane),
// open returns result.worktree.open_workspace_id. root_pane.pane_id is also
// present, so we capture it to skip a follow-up pane list.
type worktreeCreateResult struct {
	Result struct {
		Workspace struct {
			WorkspaceID string `json:"workspace_id"`
		} `json:"workspace"`
		RootPane struct {
			WorkspaceID string `json:"workspace_id"`
			PaneID      string `json:"pane_id"`
			Cwd         string `json:"cwd"`
		} `json:"root_pane"`
		Worktree struct {
			OpenWorkspaceID string `json:"open_workspace_id"`
			CheckoutPath    string `json:"checkout_path"`
		} `json:"worktree"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) WorktreeCreate(branch, cwd string) (workspaceID, checkoutPath string, err error) {
	args := []string{"worktree", "create", "--no-focus", "--branch", branch, "--cwd", cwd}
	out, err := exec.Command(r.herdrBin, args...).Output()
	if err != nil {
		return "", "", fmt.Errorf("herdr worktree create --branch %s: %w\noutput: %s", branch, err, out)
	}
	wsID, err := parseWorktreeID(out, "herdr worktree create")
	if err != nil {
		return "", "", err
	}
	var result worktreeCreateResult
	_ = json.Unmarshal(out, &result) // already parsed cleanly above
	path := result.Result.Worktree.CheckoutPath
	if path == "" {
		path = result.Result.RootPane.Cwd
	}
	if path == "" {
		return "", "", fmt.Errorf("herdr worktree create: no checkout path in output: %s", out)
	}
	return wsID, path, nil
}

// TrustDir pre-seeds Claude folder-trust for a worktree dir via
// `capataz trust-dir <path>` so the dispatched agent skips the "trust this
// folder?" prompt (which --dangerously-skip-permissions does not bypass).
func (r *realRunner) TrustDir(path string) error {
	out, err := exec.Command(r.capatazBin, "trust-dir", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("capataz trust-dir %s: %w\n%s", path, err, out)
	}
	return nil
}

func (r *realRunner) WorktreeDirty(path string) (bool, error) {
	out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
	if err != nil {
		return false, fmt.Errorf("git -C %s status --porcelain: %w", path, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (r *realRunner) IssueStatus(path string) (string, error) {
	return issues.ReadStatus(path)
}

func (r *realRunner) IssueQuestion(path string) (string, error) {
	q, _, err := issues.LatestQuestion(path)
	return q, err
}

func (r *realRunner) IssueAnswer(path string) (string, int, error) {
	return issues.LatestAnswer(path)
}

func (r *realRunner) IssueExchanges(path string) ([]issues.Exchange, error) {
	return issues.Exchanges(path)
}

func (r *realRunner) Notify(title, body string) error {
	out, err := exec.Command(r.herdrBin, "notification", "show", title, "--body", body, "--sound", "request").CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr notification show: %w\n%s", err, out)
	}
	return nil
}

// DeleteBranch deletes <branch> in repo (git -C repo branch -D). Used to clean
// up after a failed dispatch so a re-scan doesn't collide with a stale branch.
func (r *realRunner) DeleteBranch(repo, branch string) error {
	out, err := exec.Command("git", "-C", repo, "branch", "-D", branch).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git -C %s branch -D %s: %w\n%s", repo, branch, err, out)
	}
	return nil
}

func (r *realRunner) OpenBaseWorkspace(cwd string) (string, error) {
	args := []string{"workspace", "create", "--no-focus", "--cwd", cwd}
	out, err := exec.Command(r.herdrBin, args...).Output()
	if err != nil {
		return "", fmt.Errorf("herdr workspace create --cwd %s: %w\noutput: %s", cwd, err, out)
	}
	return parseWorktreeID(out, "herdr workspace create")
}

// parseWorktreeID extracts the workspace_id from a herdr worktree create/open
// JSON response. Shared by WorktreeCreate and WorktreeOpen.
func parseWorktreeID(out []byte, op string) (string, error) {
	var result worktreeCreateResult
	if err := json.Unmarshal(out, &result); err != nil {
		return "", fmt.Errorf("%s: parse output: %w\nraw: %s", op, err, out)
	}
	if result.Error != nil {
		return "", fmt.Errorf("%s: %s: %s", op, result.Error.Code, result.Error.Message)
	}
	wsID := result.Result.Workspace.WorkspaceID
	if wsID == "" {
		wsID = result.Result.RootPane.WorkspaceID
	}
	if wsID == "" {
		wsID = result.Result.Worktree.OpenWorkspaceID
	}
	if wsID == "" {
		return "", fmt.Errorf("%s: no workspace_id in output: %s", op, out)
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

// allowedTools is the tool allowlist handed to a dispatched Claude in default
// posture (no --dangerously-skip-permissions, so no bypass-permissions screen).
// This exact set was proven sufficient for capataz-work to implement + commit +
// resolve an issue fully hands-off.
const allowedTools = "Bash,Edit,Write,Read,Glob,Grep"

func (r *realRunner) AgentStart(name, paneID string, skipPerms bool) error {
	// Launch claude with the permission posture but NO task prompt: --allowedTools
	// is variadic and would swallow a trailing positional prompt, and the fresh
	// claude needs the prompt submitted separately anyway (see AgentPrompt).
	// Default posture is --allowedTools (avoids the bypass-permissions warning
	// screen that a fresh --dangerously-skip-permissions process blocks on);
	// skipPerms keeps the old bypass flag for operators who opt in.
	args := []string{"agent", "start", name, "--kind", "claude", "--pane", paneID, "--"}
	if skipPerms {
		args = append(args, "--dangerously-skip-permissions")
	} else {
		args = append(args, "--allowedTools", allowedTools)
	}
	// A freshly-created worktree pane isn't at its interactive shell prompt the
	// instant herdr returns from `worktree create`; agent start rejects it with
	// "not an available shell" (agent_pane_busy) until the shell settles. Retry
	// with a short backoff on exactly that condition — other errors fail fast.
	var out []byte
	var err error
	for attempt := 0; attempt < 15; attempt++ {
		out, err = exec.Command(r.herdrBin, args...).CombinedOutput()
		if err == nil {
			return nil
		}
		if !strings.Contains(string(out), "not an available shell") &&
			!strings.Contains(string(out), "agent_pane_busy") {
			break // a genuinely different failure — don't spin on it
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("herdr agent start %s: %w\n%s", name, err, out)
	}
	return nil
}

// AgentPrompt submits the task prompt to an already-started agent
// (`herdr agent prompt <name> <text>`), separate from AgentStart. A fresh claude
// launched via agent start sits idle at its prompt; this is what actually kicks
// off the work.
func (r *realRunner) AgentPrompt(name, prompt string) error {
	out, err := exec.Command(r.herdrBin, "agent", "prompt", name, prompt).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr agent prompt %s: %w\n%s", name, err, out)
	}
	return nil
}

// AgentReachedWorking reports whether the agent left its initial idle state
// (reached working/done/blocked) within a short window — i.e. it actually
// received and acted on the submitted prompt. A fresh claude can drop a prompt
// submitted before it's ready and sit idle; the caller retries when this is false.
func (r *realRunner) AgentReachedWorking(name string) bool {
	err := exec.Command(r.herdrBin, "agent", "wait", name,
		"--until", "working", "--until", "done", "--until", "blocked",
		"--timeout", "15000").Run()
	return err == nil
}

// agentListResult parses the subset of `herdr agent list` output AgentTurn needs.
type agentListResult struct {
	Result *struct {
		Agents []struct {
			Name          string `json:"name"`
			PaneID        string `json:"pane_id"`
			AgentStatus   string `json:"agent_status"`
			CompletionSeq int    `json:"completion_seq"`
		} `json:"agents"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) AgentTurn(name string) (string, int, error) {
	out, err := exec.Command(r.herdrBin, "agent", "list").Output()
	if err != nil {
		return "", 0, fmt.Errorf("herdr agent list: %w\noutput: %s", err, out)
	}
	return parseAgentTurn(name, out)
}

// parseAgentTurn finds name (an agent name or pane id) in `herdr agent list`
// output and returns its status and completion_seq.
func parseAgentTurn(name string, out []byte) (string, int, error) {
	var result agentListResult
	if err := json.Unmarshal(out, &result); err != nil {
		return "", 0, fmt.Errorf("herdr agent list: parse output: %w\nraw: %s", err, out)
	}
	if result.Error != nil {
		return "", 0, fmt.Errorf("herdr agent list: %s: %s", result.Error.Code, result.Error.Message)
	}
	if result.Result != nil {
		for _, a := range result.Result.Agents {
			if a.Name == name || a.PaneID == name {
				return a.AgentStatus, a.CompletionSeq, nil
			}
		}
	}
	return statusGone, 0, nil
}

// MergeResolved reports whether repo has no in-progress conflicted merge — no
// .git/MERGE_HEAD and no unmerged (conflicted) files. Used to verify a
// fix-conflict agent actually completed the merge before declaring success.
func (r *realRunner) MergeResolved(repo string) (bool, error) {
	if err := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", "MERGE_HEAD").Run(); err == nil {
		return false, nil // MERGE_HEAD present → still mid-merge
	}
	out, err := exec.Command("git", "-C", repo, "diff", "--name-only", "--diff-filter=U").Output()
	if err != nil {
		return false, fmt.Errorf("git -C %s diff --diff-filter=U: %w", repo, err)
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// ClosePane closes a herdr pane (used to tear down a base-repo workspace opened
// for a fix-conflict agent).
func (r *realRunner) ClosePane(paneID string) error {
	out, err := exec.Command(r.herdrBin, "pane", "close", paneID).CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr pane close %s: %w\n%s", paneID, err, out)
	}
	return nil
}

// statusGone is the AgentWait/AgentUnblocked status for an agent herdr no
// longer knows (herdr's agent_not_found).
const statusGone = "gone"

// agentWaitResult parses the subset of herdr agent wait output we need.
type agentWaitResult struct {
	Result *struct {
		Agent *struct {
			AgentStatus string `json:"agent_status"`
		} `json:"agent"`
		Status string `json:"status"`
		State  string `json:"state"`
	} `json:"result"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (r *realRunner) AgentWait(target string, timeoutMS int) (string, error) {
	// Callers confirm the agent actually started (AgentReachedWorking) before
	// this, so idle here means "finished". Wait for the agent to end its turn.
	// herdr reports "done" for an UNFOCUSED tab but "idle" for a focused one
	// (same underlying finished state), so accept both — else a drain hangs
	// whenever an operator clicks into the pane. "blocked" means it needs input.
	return r.agentWait(target, timeoutMS, "done", "idle", "blocked")
}

func (r *realRunner) AgentUnblocked(target string, timeoutMS int) (string, error) {
	return r.agentWait(target, timeoutMS, "working", "done", "idle")
}

func (r *realRunner) AgentResumed(target string, timeoutMS int) (string, error) {
	return r.agentWait(target, timeoutMS, "working", "blocked")
}

// agentWait runs `herdr agent wait <target> --until <s>... --timeout <ms>` and
// returns the state it stopped on, with idle folded into done.
func (r *realRunner) agentWait(target string, timeoutMS int, until ...string) (string, error) {
	args := []string{"agent", "wait", target}
	for _, u := range until {
		args = append(args, "--until", u)
	}
	args = append(args, "--timeout", fmt.Sprintf("%d", timeoutMS))
	out, err := exec.Command(r.herdrBin, args...).Output()
	var stderr []byte
	if ee, ok := err.(*exec.ExitError); ok {
		stderr = ee.Stderr
	}
	return parseAgentWait(target, out, stderr, err)
}

// parseAgentWait maps `herdr agent wait` output to the state it stopped on,
// with idle folded into done. herdr reports failures (timeout, missing agent)
// as a JSON error on stdout with a non-zero exit, so the error code is read
// before the exit status.
func parseAgentWait(target string, out, stderr []byte, runErr error) (string, error) {
	var result agentWaitResult
	jsonErr := json.Unmarshal(out, &result)
	if jsonErr == nil && result.Error != nil {
		switch result.Error.Code {
		case "timeout":
			return "timeout", nil
		case "agent_not_found":
			return statusGone, nil
		}
		return "error", fmt.Errorf("herdr agent wait %s: %s: %s", target, result.Error.Code, result.Error.Message)
	}
	if runErr != nil {
		return "error", fmt.Errorf("herdr agent wait %s: %w\noutput: %s%s", target, runErr, out, stderr)
	}
	if jsonErr != nil {
		// Non-JSON or partial output — treat as done if command succeeded
		return "done", nil
	}
	if result.Result != nil {
		var s string
		if result.Result.Agent != nil {
			s = result.Result.Agent.AgentStatus // herdr 0.9+
		}
		if s == "" {
			s = result.Result.Status
		}
		if s == "" {
			s = result.Result.State
		}
		if s == "idle" {
			s = "done" // a finished (focused) agent — same as done for the drain
		}
		if s != "" {
			return s, nil
		}
	}
	// Command succeeded; assume done.
	return "done", nil
}

func (r *realRunner) Merge(branch, repo string) (bool, error) {
	out, err := exec.Command("git", "-C", repo, "merge", "--no-ff", branch).CombinedOutput()
	if err == nil {
		return false, nil
	}
	// Inspect unmerged files to distinguish a conflict from a hard failure.
	uf, _ := exec.Command("git", "-C", repo, "diff", "--name-only", "--diff-filter=U").Output()
	if strings.TrimSpace(string(uf)) != "" {
		// Conflicted files present — mid-merge state for a fix-conflict agent.
		return true, nil
	}
	return false, fmt.Errorf("git merge %s: %s", branch, strings.TrimSpace(string(out)))
}

func (r *realRunner) WorktreeRemove(workspaceID string) error {
	// --force so a worktree with any residual state (or a just-merged one herdr
	// considers non-empty) still tears down cleanly.
	out, err := exec.Command(r.herdrBin, "worktree", "remove", "--workspace", workspaceID, "--force").CombinedOutput()
	if err != nil {
		return fmt.Errorf("herdr worktree remove --workspace %s --force: %w\n%s", workspaceID, err, out)
	}
	return nil
}
