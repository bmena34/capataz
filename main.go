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
//	capataz uninstall    remove hooks + launchd agent
package main

import (
	"fmt"
	"os"

	"github.com/bmena34/capataz/internal/config"
	"github.com/bmena34/capataz/internal/daemon"
	"github.com/bmena34/capataz/internal/hook"
	"github.com/bmena34/capataz/internal/install"
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
		run(install.Install)
	case "uninstall":
		logging.SetComponent("install")
		run(install.Uninstall)
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
	case "config":
		run(showConfig)
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
