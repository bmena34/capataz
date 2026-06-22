<p align="center">
  <img src="docs/logo.svg" alt="capataz" width="600">
</p>

<p align="center">
  A terminal dashboard for supervising your <a href="https://claude.com/claude-code">Claude&nbsp;Code</a> agents in tmux —<br>
  it surfaces which agent needs you and lets you switch to, reply to, rename, or kill any session.
</p>

<p align="center"><em>capataz</em> is Spanish for <em>foreman</em> — it keeps the crew of agents in line.</p>

<!-- Save a dashboard screenshot to docs/screenshot.png and uncomment:
<p align="center"><img src="docs/screenshot.png" alt="capataz dashboard" width="340"></p>
-->

## Why

When you run several Claude Code agents at once, it's hard to tell which one is blocked on you. `capataz` watches them all through Claude Code's hooks and gives you one place to see status and jump in — either as a quick **popup switcher** or a persistent **sidebar** docked next to whatever you're working on.

## Requirements

- **macOS** (uses the macOS Keychain and `launchd`)
- **tmux** — agents run in tmux panes; the dashboard drives them via tmux
- **Claude Code** — `capataz` wires into its hooks
- **Go 1.24+** — to build/install

## Install

```sh
go install github.com/bmena34/capataz@latest   # builds the `capataz` binary into $GOBIN
capataz install                                 # wire Claude Code hooks + start the daemon
```

Or build from source:

```sh
git clone https://github.com/bmena34/capataz && cd capataz
go build -o capataz . && go install .
capataz install
```

`capataz install` merges hooks into `~/.claude/settings.json` (backing up the original), installs a `launchd` agent for the background daemon, and writes a default config. **Hooks load when a Claude session starts**, so restart any sessions you already have running.

Then bind the tmux keys (live + prints lines for your `~/.tmux.conf`):

```sh
capataz keybind          # prefix+g — summon the dashboard popup
capataz keybind attach   # prefix+a — dock a dashboard sidebar in the current window
capataz keybind nav      # Ctrl+h/j/k/l — move between panes (nvim-aware)
capataz statusline       # optional: show a "needs you" badge in your tmux status bar
```

## Usage

- **`prefix + g`** — open the dashboard as a floating popup (a switcher). Pick a session with `Enter` and it switches you there.
- **`prefix + a`** — dock the dashboard as a sidebar in the current window. `Ctrl+h`/`Ctrl+l` move between the sidebar and your work.
- **`n`** — spawn a new Claude session (prompts for directory, then name); it comes up with its own sidebar.

Keys inside the dashboard:

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | move selection |
| `Enter` | switch to the selected session |
| `n` | new session |
| `r` | quick reply without leaving the dashboard |
| `c` | conversation view |
| `R` | rename the session |
| `l` | event log |
| `x` | quit a session (graceful, with confirm) |
| `?` | full keybinding help |
| `q` | close the dashboard |

## How it works

- **Hooks** — Claude Code fires hooks (SessionStart, PreToolUse, Stop, Notification, SubagentStop, …) that run `capataz hook <kind>`, reporting each event (with its tmux pane) to the daemon.
- **Daemon** — a small `launchd`-managed background process keeps per-session state, prunes sessions whose tmux pane has died, and pushes live updates to every open dashboard over a Unix socket.
- **Dashboard** — a [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI. You can run several at once (one per sidebar plus a popup); they all read the same daemon state.

It tracks **only tmux sessions** — the ones it can actually act on. Task subagents share their parent's session, so the dashboard shows a `⊂N` count of running subagents on the parent row rather than separate entries.

## Configuration

Optional, at `~/.config/capataz/config.json` (created by `capataz install`; `capataz config` prints it):

```json
{
  "usage": {
    "enabled": true,
    "url": "https://api.anthropic.com/api/oauth/usage",
    "keychain_service": "Claude Code-credentials",
    "poll_seconds": 300
  },
  "sidebar": { "width": 40 }
}
```

> **Note on the usage gauge.** To show your plan usage (current-session and weekly %), `capataz` reads your Claude Code OAuth token from the macOS Keychain and calls `api/oauth/usage` — the **same endpoint Claude Code's `/usage` uses**. This is *unofficial* and undocumented: it may change or stop working at any time. It only ever reads *your* token to display *your* usage; nothing is sent anywhere else. Set `usage.enabled` to `false` to turn it off entirely, or update `url` / `keychain_service` if Anthropic changes them.

`CAPATAZ_CONTEXT_LIMIT` (e.g. `1m`, `200k`) overrides the context-window size used for the per-session gauge. `CAPATAZ_DEBUG=1` enables verbose logging (`capataz logs`).

## Commands

```
capataz                open the dashboard
capataz popup          open the dashboard in a tmux popup (switcher)
capataz attach         dock a dashboard sidebar in the current tmux window
capataz keybind [k]    bind prefix+k (default g) to the popup; also `attach` / `nav`
capataz statusline     show a "needs you" badge in tmux (off to remove)
capataz config         print the config file path + contents
capataz install        wire Claude Code hooks + start the daemon
capataz uninstall      remove hooks + stop the daemon
capataz status         show agent + daemon state
capataz logs           print the application log
capataz quit           stop running dashboards
```

## Uninstall

```sh
capataz uninstall   # removes hooks + the launchd daemon
```

## License

[MIT](LICENSE) © Brandon Mena
