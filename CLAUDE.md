# CLAUDE.md

Guidance for working on **capataz** with Claude Code. User-facing docs are in `README.md`; this file is the contributor/agent map: architecture, the dev loop, and the non-obvious gotchas.

## What it is

A terminal dashboard that supervises Claude Code agents running in tmux. **macOS-only** (it uses the macOS Keychain via `security` and `launchd` for the daemon). Pure Go, single binary with subcommands.

## Build / test / run

```sh
go build -o capataz .                       # build the binary
go vet ./...                                # vet
go test ./...                               # tests
go test ./internal/tui -run TestRenderNoPanic   # render regression (every size 1×1..80×50)
```

The binary is multiplexed by subcommand (`main.go`): default → dashboard; `daemon`, `hook`, `install`, `uninstall`, `status`, `logs`, `quit`, `config`, `popup`, `attach`, `keybind`, `statusline`.

**Dev loop — what to do after changing each component:**
- **daemon code** (`internal/daemon`): `go build -o capataz . && ./capataz restart`. The running daemon is a snapshot of the binary at launch.
- **dashboard code** (`internal/tui`): rebuild, then relaunch the dashboard. The **popup re-runs the binary fresh on every `prefix+g`**, so it picks up changes immediately; **sidebars** are long-lived and must be re-attached (`prefix+a`).
- **hook code** (`internal/hook`): rebuilt binary is re-exec'd on every hook call, so changes apply immediately. But **adding/removing a hook event** requires `./capataz install` (edits `~/.claude/settings.json`) **and** restarting any Claude sessions — hooks load at session start.

Debug: `./capataz status`, `./capataz logs` (app log at `~/.capataz/capataz.log`), `CAPATAZ_DEBUG=1` for verbose.

## Architecture

Data flows **hook → daemon → dashboard(s)**:

- **`internal/hook`** — `capataz hook <kind>` is invoked by Claude Code hooks. Reads the hook JSON from stdin plus `$TMUX_PANE`, sends a `proto.Event` to the daemon over a Unix socket (`~/.capataz/capataz.sock`). Must be fast and never block Claude.
- **`internal/daemon`** — one `launchd`-managed background process. Maps events → per-session state, polls plan usage centrally, prunes dead-pane sessions (5s sweep), and broadcasts `proto.Push` to every connected dashboard. Persists sessions + event history to SQLite (`~/.capataz/history.db`, `internal/store`).
- **`internal/tui`** — the Bubble Tea dashboard. Two launch modes: **popup** switcher (`CAPATAZ_POPUP=1`, transient — selecting quits) and **sidebar** (a normal pane — selecting switches without quitting). Several can run at once; all subscribe to the same daemon state. Acts on sessions purely through tmux (`tmux.go`: jump/reply/kill-window/rename/new-session/attach).
- **`internal/proto`** — wire types shared by all three (`Event`, `Session`, `Push`, `Usage`, state enum).
- Support: **`internal/paths`** (on-disk locations + `dev.capataz` launchd label), **`internal/config`** (`~/.config/capataz/config.json`), **`internal/install`** (hook wiring + launchd plist), **`internal/logging`**.

## Gotchas (read before changing things)

- **Tracks only tmux sessions.** The daemon ignores a new session with no tmux pane, and the sweep drops sessions whose pane died. Task **subagents share their parent's `session_id`** (they're in-session sidechains) — so they can't be separate rows; the dashboard shows a `⊂N` count on the parent. The subagent-spawn tool is named **`Agent`** in current Claude Code (older: `Task`) — match both.
- **Popups have no `$TMUX_PANE`.** Anything that must work from the popup (e.g. new-session) must not depend on `TMUX_PANE` — gate on `$TMUX`. For client dimensions in a popup use `clientSize()` (`tmux list-clients`), not `display-message -p` (returns empty there).
- **tmux resizes panes proportionally.** The sidebar self-pins to its config width on a window resize (`pinSidebar`); new sessions are created at the client's real size so the split isn't 50/50.
- **Render must never panic.** Negative width/height math (slices, `strings.Repeat`) crashes the TUI, which closes its tmux pane (looks like "the sidebar disappeared"). Guard them; `TestRenderNoPanic` is the safety net.
- **Killing a session** sends `/quit` (graceful, fires `SessionEnd`) then `kill-window` after a grace period (tears down the sidebar too). If you're killing the session you're attached to, it hops the client to another managed session first.
- **launchd PATH**: the plist bakes the user's PATH (+ Homebrew) so the daemon can run `tmux`. Re-run `./capataz install` after PATH-relevant changes.
- **Usage gauge is unofficial.** The daemon reads the Claude Code OAuth token from the Keychain and calls `api/oauth/usage` (the endpoint `/usage` uses). URL + keychain item + interval + on/off are all in `config`; never hardcode them, and keep the "may change / disable-able" framing.
- **No external image/CLI deps** beyond the Go modules in `go.mod` (Bubble Tea, Lipgloss, modernc sqlite). Keep it dependency-light.
