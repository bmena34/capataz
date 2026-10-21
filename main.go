// Command capataz is the workflow layer for Claude Code agents running under
// herdr: a per-repo .scratch issue backlog, parallel-safe dispatch waves, a
// hands-off drain orchestrator, and the skills that drive them. The repo is
// also the herdr plugin (herdr-plugin.toml); its panes and actions call these
// subcommands.
package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/drain"
	"github.com/bmena34/capataz/internal/install"
	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/issueview"
	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/tfscan"
	"github.com/bmena34/capataz/internal/usageview"
)

const version = "0.2.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		return
	}

	switch args[0] {
	case "install":
		run(func() error { return install.Install(skillsSub(), agentsSub()) })
	case "uninstall":
		run(func() error { return install.Uninstall(skillsSub(), agentsSub()) })
	case "issue":
		run(func() error { return issues.Run(args[1:]) })
	case "issues":
		// Full-screen backlog viewer for the repo at cwd, or an explicit path arg.
		cwd := ""
		if len(args) > 1 {
			cwd = args[1]
		}
		run(func() error { return issueview.Run(cwd) })
	case "drain":
		run(func() error { return drain.Run(args[1:]) })
	case "tf":
		// Exit codes are part of the contract: 0 clean, 1 findings, 2 error.
		os.Exit(tfscan.Run(args[1:]))
	case "usage":
		run(runUsage)
	case "usage-view":
		run(func() error { return usageview.Run(args[1:]) })
	case "trust-dir":
		// Pre-seed Claude folder-trust for a directory (used by the drain before
		// dispatching an agent into a fresh worktree).
		run(func() error { return runTrustDir(args[1:]) })
	case "config":
		run(showConfig)
	case "version", "-v", "--version":
		fmt.Println("capataz", version)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

// skillsSub returns the embedded skills tree rooted at the skill directories
// (so its top-level entries are the skill names). Returns nil if the embed is
// somehow unreadable, which makes install/uninstall skip skills rather than fail.
func skillsSub() fs.FS {
	sub, err := fs.Sub(skillsFS, "skills")
	if err != nil {
		return nil
	}
	return sub
}

// agentsSub returns the embedded agents tree rooted at the agent directories
// (so its top-level entries are the agent names). Returns nil if the embed is
// somehow unreadable, which makes install/uninstall skip agents rather than fail.
func agentsSub() fs.FS {
	sub, err := fs.Sub(agentsFS, "agents")
	if err != nil {
		return nil
	}
	return sub
}

// showConfig ensures a default config exists and prints its path + contents.
func showConfig() error {
	if err := config.WriteDefault(); err != nil {
		return err
	}
	fmt.Println(paths.ConfigFile())
	data, err := os.ReadFile(paths.ConfigFile())
	if err != nil {
		return err
	}
	fmt.Println()
	os.Stdout.Write(data)
	return nil
}

func run(f func() error) {
	if err := f(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`capataz — issue backlog + drain orchestration for Claude Code under herdr

usage:
  capataz issues [dir]        full-screen backlog viewer for the repo at dir (default cwd)
  capataz issue …             manage the .scratch backlog: claim/resolve/status/comment/list/new/waves (used by skills)
  capataz drain [flags] <f>   drain feature f's backlog: dispatch parallel-safe waves of agents via herdr
  capataz tf check [flags]    Terraform race checks vs open PRs, sibling worktrees, base (exit 0/1/2)
  capataz usage               print the plan-usage gauge (reads Keychain token; unofficial endpoint)
  capataz usage-view [flags]  live plan-usage + per-agent context popup (herdr usage pane)
  capataz trust-dir <path>    pre-seed Claude folder-trust for path
  capataz config              print the config file path + contents (~/.config/capataz)
  capataz install             install skills + agents, default config, global .scratch ignore
  capataz uninstall           remove the installed skills + agents
  capataz version             print version
`)
}
