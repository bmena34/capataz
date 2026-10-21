// Package drain is the hands-off drain orchestrator behind `capataz drain`
// (the herdr plugin's drain action).
//
// It scans a feature's issue backlog via `capataz issue waves --json`, takes
// a parallel-safe wave of ready issues up to the concurrency cap, dispatches a
// Claude agent per issue into a herdr worktree, and re-scans until the backlog
// is drained.
//
// Usage:
//
//	capataz drain [flags] <feature>
//
// Flags:
//
//	-concurrency  int    max concurrent agents per wave (0 = unlimited, default 3)
//	-timeout      int    per-agent wait timeout in milliseconds (default 3600000)
//	-resume-grace int    ms a worker that ended its turn unfinished gets to resume (default 180000)
//	-skip-perms         pass --dangerously-skip-permissions to dispatched agents
//	-repo         string repo path for worktree creation (default: cwd)
//	-branch-prefix string  branch name prefix (overrides config drain.branch_prefix)
//	-dry-run            print plan without creating worktrees or starting agents
//
// Environment:
//
//	CAPATAZ_BIN   path to the capataz binary (default: this executable)
//	HERDR_BIN     path to the herdr binary   (default "herdr")
package drain

import (
	"flag"
	"fmt"
	"os"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/worktree"
)

// Run parses args and runs the command; it is `capataz drain`.
func Run(args []string) error {
	fs := flag.NewFlagSet("drain", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	concurrency := fs.Int("concurrency", 3, "max concurrent agents per wave (0 = unlimited)")
	timeoutMS := fs.Int("timeout", 3_600_000, "per-agent wait timeout in milliseconds")
	resumeGraceMS := fs.Int("resume-grace", defaultResumeGraceMS, "milliseconds a worker that ended its turn with unfinished work gets to start working again")
	skipPerms := fs.Bool("skip-perms", false, "pass --dangerously-skip-permissions to dispatched agents")
	repoPath := fs.String("repo", "", "repo path for worktree creation (default: current directory)")
	branchPrefix := fs.String("branch-prefix", "", "dispatch branch prefix (default: drain.branch_prefix from config, else empty)")
	dryRun := fs.Bool("dry-run", false, "print planned dispatch without creating worktrees or starting agents")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: capataz drain [flags] <feature>\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEnvironment:\n")
		fmt.Fprintf(os.Stderr, "  CAPATAZ_BIN  path to capataz binary (default: this executable)\n")
		fmt.Fprintf(os.Stderr, "  HERDR_BIN    path to herdr binary   (default \"herdr\")\n")
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("feature argument required")
	}
	feature := fs.Arg(0)

	// `capataz issue waves` (spawned by the runner) reads the prefix from this
	// env var, so the flag wins over config without widening the Runner API.
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "branch-prefix" {
			os.Setenv("CAPATAZ_BRANCH_PREFIX", *branchPrefix)
		}
	})

	// Resolve repo path: flag takes precedence, then cwd.
	repo := *repoPath
	if repo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		repo = cwd
	}

	// Resolve binary paths from environment, falling back to defaults.
	capatazBin := selfBin()
	herdrBin := os.Getenv("HERDR_BIN")
	if herdrBin == "" {
		herdrBin = "herdr"
	}

	runner := &realRunner{
		capatazBin: capatazBin,
		herdrBin:   herdrBin,
	}

	cfg := Config{
		Feature:         feature,
		Concurrency:     *concurrency,
		SkipPermissions: *skipPerms,
		AgentTimeoutMS:  *timeoutMS,
		ResumeGraceMS:   *resumeGraceMS,
		RepoPath:        repo,
		DryRun:          *dryRun,
		Out:             os.Stdout,
		StallMinutes:    config.Load().DrainFor(configKey(repo)).StallMinutes,
	}

	return Drain(cfg, runner)
}

// configKey is the drain.repos key for repo: its primary checkout, the same
// key `capataz issue waves` uses for branch_prefix, so --repo ., a
// subdirectory or a linked worktree all hit the same entry. Falls back to repo.
func configKey(repo string) string {
	if p, err := worktree.PrimaryCheckout(repo); err == nil {
		return p
	}
	return repo
}

// selfBin is the capataz binary to shell out to: CAPATAZ_BIN if set, else the
// running executable (this command is itself a capataz subcommand).
func selfBin() string {
	if b := os.Getenv("CAPATAZ_BIN"); b != "" {
		return b
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "capataz"
}
