<p align="center">
  <img src="docs/logo.svg" alt="capataz" width="600">
</p>

<p align="center">
  A terminal dashboard for supervising your <a href="https://claude.com/claude-code">Claude&nbsp;Code</a> agents in tmux —<br>
  it surfaces which agent needs you and lets you switch to, reply to, rename, or kill any session.
</p>

<p align="center">
  <img src="docs/screenshot.png" alt="capataz dashboard docked beside a live Claude Code session" width="820">
</p>

## Why

When you run several Claude Code agents at once, it's hard to tell which one is blocked on you. `capataz` watches them all through Claude Code's hooks and gives you one place to see status and jump in — either as a quick **popup switcher** or a persistent **sidebar** docked next to whatever you're working on.

## Requirements

- **macOS** (uses the macOS Keychain and `launchd`)
- **tmux** — agents run in tmux panes; the dashboard drives them via tmux
- **Claude Code** — `capataz` wires into its hooks
- **Go 1.26+** — to build

## Install

```sh
git clone https://github.com/bmena34/capataz && cd capataz
make install        # builds and copies to /usr/local/bin (prompts for sudo password)
capataz install     # wire Claude Code hooks + start the daemon
```

`capataz install` merges hooks into `~/.claude/settings.json` (backing up the original), installs a `launchd` agent for the background daemon, installs the `capataz-work` skill into `~/.claude/skills`, and writes a default config. **Hooks load when a Claude session starts**, so restart any sessions you already have running.

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

<p align="center">
  <img src="docs/popup.png" alt="capataz popup switcher over a session" width="820">
  <br><sub>the popup switcher (<code>prefix + g</code>)</sub>
</p>

Keys inside the dashboard:

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | move selection |
| `Enter` | switch to the selected session |
| `n` | new session |
| `r` | quick reply without leaving the dashboard |
| `c` | conversation view |
| `R` | rename the session |
| `i` | issues — full-screen backlog for the selected repo, start work on one (use from the `prefix+g` popup) |
| `l` | event log |
| `x` | quit a session (graceful, with confirm) |
| `?` | full keybinding help |
| `q` | close the dashboard |

## How it works

- **Hooks** — Claude Code fires hooks (SessionStart, PreToolUse, Stop, Notification, SubagentStop, …) that run `capataz hook <kind>`, reporting each event (with its tmux pane) to the daemon.
- **Daemon** — a small `launchd`-managed background process keeps per-session state, prunes sessions whose tmux pane has died, and pushes live updates to every open dashboard over a Unix socket.
- **Dashboard** — a [Bubble Tea](https://github.com/charmbracelet/bubbletea) TUI. You can run several at once (one per sidebar plus a popup); they all read the same daemon state.

It tracks **only tmux sessions** — the ones it can actually act on. Task subagents share their parent's session, so the dashboard shows a `⊂N` count of running subagents on the parent row rather than separate entries.

## Issue tracking

`capataz` reads a per-repo backlog of markdown issues and lets you put an agent on one. The **sidebar** shows a concise status tracker — the backlog of the selected session's repo, one dot per issue, sorted attention-first (blocked / todo / doing / done) with a counts summary. To **act** on the backlog, open the **popup** (`prefix + g`) and press **`i`**: browse the issues and `Enter` on one to start work, either in a **new session** or by handing it to the session you have selected.

**The review loop.** Two optional review steps keep the work honest:

- **Per-issue sanity check** (opt-in via config `review.sanity_check`): before an agent resolves an issue, the `capataz-work` skill spawns a fresh-context subagent to check the change actually matches the issue's acceptance criteria — a guard against an agent fooling itself.
- **Fresh full review** (`R` in the issues view): when a feature's issues are all resolved, press `R` to spawn a **new session** that runs the `capataz-review` skill — a clean context with no memory of how the code was written, so it judges soundness rather than trusting the implementers. Its findings are filed straight back into the backlog as new `ready-for-agent` issues, so the loop continues (review → issues → fix → review) until a review comes back clean.

> **⚠️ Work one issue at a time (for now).** Every session capataz starts for an issue runs in the **same repo working tree**. Putting **two or more agents on issues in the same repo at once** means they edit that one working tree simultaneously — overlapping edits clobber each other with no git conflict markers to catch it, and each agent's tests run against the others' half-finished changes. Dispatch, let it finish (and review), then dispatch the next. Safe **parallel dispatch** — giving each agent its own git worktree so the work is isolated — is a planned future feature, not something to rely on today.

Issues live as plain markdown, one feature per directory:

```
<repo>/.scratch/<feature>/issues/NN-slug.md
```

Each file has a title and a few header lines near the top:

```markdown
# Add the login form

Status: ready-for-agent
Type: task
Blocked by: None

## What to build
…
```

This is the layout the [mattpocock/skills](https://github.com/mattpocock/skills) planning skills produce — use `/to-issues` to break a plan into issues and `/triage` to move them to `ready-for-agent`. `capataz install` also installs a **`capataz-work`** skill: when you start an issue, the agent runs it to claim the issue (`Status: claimed`), implement the slice, and resolve it (`Status: resolved`). The markdown files are the source of truth; the dashboard re-reads them live, so claims and resolutions show up as agents make them.

You can also drive the backlog from the command line:

```sh
capataz issue list .                       # show this repo's backlog
capataz issue claim   .scratch/f/issues/01-x.md
capataz issue resolve .scratch/f/issues/01-x.md --answer "what shipped"
```

## Configuration

Optional, at `~/.config/capataz/config.json` (created by `capataz install`; `capataz config` prints it):

```json
{
  "usage": {
    "enabled": true,
    "url": "https://api.anthropic.com/api/oauth/usage",
    "keychain_service": "Claude Code-credentials",
    "poll_seconds": 300,
    "plan_type": ""
  },
  "sidebar": { "width": 40 },
  "review": { "sanity_check": false }
}
```

`review.sanity_check` (default `false`) turns on the per-issue sanity-check subagent described above; the full fresh review is always available via `R`.

> **Note on the usage gauge.** To show your plan usage (current-session and weekly %), `capataz` reads your Claude Code OAuth token from the macOS Keychain and calls `api/oauth/usage` — the **same endpoint Claude Code's `/usage` uses**. This is *unofficial* and undocumented: it may change or stop working at any time. It only ever reads *your* token to display *your* usage; nothing is sent anywhere else. Set `usage.enabled` to `false` to turn it off entirely, or update `url` / `keychain_service` if Anthropic changes them.
>
> Set `plan_type` to `"api"` if you're on an enterprise or API-billing plan — this replaces the session/weekly bars with a "billed per token" label. Leave blank to auto-detect from the API response.

`CAPATAZ_CONTEXT_LIMIT` (e.g. `1m`, `200k`) overrides the context-window size used for the per-session gauge. `CAPATAZ_DEBUG=1` enables verbose logging (`capataz logs`).

## Commands

```
capataz                open the dashboard
capataz popup          open the dashboard in a tmux popup (switcher)
capataz attach         dock a dashboard sidebar in the current tmux window
capataz keybind [k]    bind prefix+k (default g) to the popup; also `attach` / `nav`
capataz statusline     show a "needs you" badge in tmux (off to remove)
capataz config         print the config file path + contents
capataz issue …        claim/resolve/list local .scratch issues (used by skills)
capataz install        wire hooks, install skills + start the daemon
capataz uninstall      remove hooks, skills + stop the daemon
capataz status         show agent + daemon state
capataz logs           print the application log
capataz quit           stop running dashboards
```

## Uninstall

```sh
capataz uninstall   # removes hooks + the launchd daemon
```

## License

[MIT](LICENSE)
