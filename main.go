// Command capataz is a terminal dashboard for supervising Claude Code sessions
// running in tmux — it surfaces which agent needs you, and lets you jump to or
// reply to it without losing your place.
//
// Subcommands:
//
//	capataz             open the dashboard (default)
//	capataz daemon      run the background state daemon (launchd-managed)
//	capataz hook <kind> report a Claude Code hook event (used internally)
//	capataz install     wire up hooks + launchd agent
//	capataz uninstall   remove hooks + launchd agent
//	capataz usage       print the live plan-usage gauge (reads Keychain; unofficial endpoint)
package main

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/daemon"
	"github.com/bmena34/capataz/internal/hook"
	"github.com/bmena34/capataz/internal/install"
	"github.com/bmena34/capataz/internal/issues"
	"github.com/bmena34/capataz/internal/logging"
	"github.com/bmena34/capataz/internal/paths"
	"github.com/bmena34/capataz/internal/tui"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		logging.SetComponent("tui")
		run(tui.Run)
		return
	}

	switch args[0] {
	case "daemon":
		logging.SetComponent("daemon")
		run(daemon.Run)
	case "hook":
		logging.SetComponent("hook")
		if len(args) < 2 {
			// Never block Claude on a malformed hook invocation.
			os.Exit(0)
		}
		_ = hook.Run(args[1])
		os.Exit(0)
	case "install":
		logging.SetComponent("install")
		run(func() error { return install.Install(skillsSub(), agentsSub()) })
	case "uninstall":
		logging.SetComponent("install")
		run(func() error { return install.Uninstall(skillsSub(), agentsSub()) })
	case "start":
		logging.SetComponent("install")
		run(install.Start)
	case "stop":
		logging.SetComponent("install")
		run(install.Stop)
	case "restart":
		logging.SetComponent("install")
		run(install.Restart)
	case "status":
		run(install.Status)
	case "popup":
		logging.SetComponent("tui")
		run(tui.LaunchPopup)
	case "keybind":
		logging.SetComponent("tui")
		// capataz keybind [popup|attach|nav] [key]   (bare form binds the popup)
		what, key := "popup", ""
		rest := args[1:]
		if len(rest) > 0 && (rest[0] == "popup" || rest[0] == "attach" || rest[0] == "nav") {
			what, rest = rest[0], rest[1:]
		}
		if len(rest) > 0 {
			key = rest[0]
		}
		switch what {
		case "attach":
			run(func() error { return tui.InstallAttachKeybind(key) })
		case "nav":
			run(tui.InstallNavKeybind)
		default:
			run(func() error { return tui.InstallKeybind(key) })
		}
	case "statusline":
		logging.SetComponent("tui")
		if len(args) > 1 && args[1] == "off" {
			run(tui.DisableStatusline)
		} else {
			run(tui.EnableStatusline)
		}
	case "attach":
		logging.SetComponent("tui")
		pane := ""
		if len(args) > 1 {
			pane = args[1]
		}
		run(func() error { return tui.Attach(pane) })
	case "issue":
		run(func() error { return issues.Run(args[1:]) })
	case "issues":
		// Standalone, daemon-free full-screen backlog viewer (no tmux, no daemon).
		// Scopes to the repo at cwd, or an explicit path arg. Used by the herdr
		// plugin pane and usable in any bare terminal.
		logging.SetComponent("tui")
		cwd := ""
		if len(args) > 1 {
			cwd = args[1]
		}
		run(func() error { return tui.RunIssueView(cwd) })
	case "config":
		run(showConfig)
	case "usage":
		run(runUsage)
	case "trust-dir":
		// Pre-seed Claude folder-trust for a directory (used by the herdr drain
		// before dispatching an agent into a fresh worktree).
		run(func() error { return runTrustDir(args[1:]) })
	case "quit":
		run(tui.Quit)
	case "logs":
		run(showLogs)
	case "watch", "tui":
		logging.SetComponent("tui")
		run(tui.Run)
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

// showLogs prints the shared application log.
func showLogs() error {
	data, err := os.ReadFile(paths.AppLog())
	if os.IsNotExist(err) {
		fmt.Println("no log yet at", paths.AppLog())
		return nil
	}
	if err != nil {
		return err
	}
	os.Stdout.Write(data)
	fmt.Fprintf(os.Stderr, "\n(follow live with: tail -f %s)\n", paths.AppLog())
	return nil
}

func run(f func() error) {
	if err := f(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`capataz — supervise your Claude Code sessions

usage:
  capataz             open the dashboard
  capataz popup       open the dashboard in a tmux popup (switcher mode)
  capataz attach      add a dashboard sidebar to the current tmux window
  capataz keybind [k]          bind prefix+k (default g) to summon the popup
  capataz keybind attach [k]   bind prefix+k (default a) to attach a sidebar
  capataz keybind nav          bind Ctrl+h/j/k/l pane nav (nvim-aware)
  capataz statusline  show a "needs you" badge in tmux (off to remove)
  capataz usage       print the plan-usage gauge (reads Keychain token; unofficial endpoint)
  capataz issue …     manage the local .scratch backlog: claim/resolve/status/comment/list/new (used by skills)
  capataz config      print the config file path + contents (~/.config/capataz)
  capataz install     wire up Claude Code hooks + start the daemon
  capataz uninstall   remove hooks + stop the daemon
  capataz start       start the daemon
  capataz stop        stop the daemon (no respawn)
  capataz restart     restart the daemon
  capataz status      show agent + daemon state
  capataz quit        stop a running dashboard process
  capataz logs        print the application log
  capataz daemon      run the daemon in the foreground
  capataz version     print version
`)
}
