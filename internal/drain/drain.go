package drain

import (
	"io"
	"time"
)

// Config holds all knobs for the drain orchestrator.
type Config struct {
	// Feature is the .scratch feature slug to drain (e.g. "herdr-port").
	Feature string

	// Concurrency is the maximum number of agents running in parallel per wave.
	// 0 means unlimited (take the full wave at once).
	Concurrency int

	// SkipPermissions controls whether dispatched agents run with
	// --dangerously-skip-permissions. Default false: permission prompts surface
	// as herdr "blocked" and the loop surfaces them for operator attention.
	SkipPermissions bool

	// AgentTimeoutMS is the per-agent wait timeout in milliseconds.
	// Default 3600000 (1 hour).
	AgentTimeoutMS int

	// RepoPath is the absolute path to the base repository. Used as --cwd for
	// herdr worktree create so the new worktree is rooted in the correct repo.
	RepoPath string

	// DryRun prints the planned dispatch without creating worktrees or starting
	// agents. Safe manual verification path.
	DryRun bool

	// Out is where drain writes status messages. Defaults to os.Stdout.
	Out io.Writer

	// askPoll is how often an asking issue's status is re-read (default 15s);
	// now/sleep are the clock (default time.Now/time.Sleep). Tests inject fakes.
	askPoll time.Duration
	now     func() time.Time
	sleep   func(time.Duration)
}

// IssueResult records the outcome of dispatching a single issue.
type IssueResult struct {
	Issue  WaveIssue
	Status string // "done", "blocked", "timeout", "error"
	Err    error
}
