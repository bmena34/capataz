<p align="center">
  <img src="docs/logo.svg" alt="capataz" width="600">
</p>

<p align="center">
  An opinionated tmux workflow for shipping software with <a href="https://claude.com/claude-code">Claude&nbsp;Code</a> agents.<br>
  Plan a feature, split it into issues, drain the backlog with parallel agents in isolated worktrees,<br>
  review it with fresh eyes — and always know which agent needs you.
</p>

<p align="center">
  <img src="docs/screenshot.png" alt="capataz dashboard docked beside a live Claude Code session" width="820">
</p>

*Capataz* is Spanish for **foreman**: it runs the crew so you can run the job.

## Why

Running one Claude Code agent is easy. Running several is where it falls apart:

- You can't tell which agent is **blocked on you** — one is waiting on a permission prompt while you watch another one think.
- Two agents in the **same checkout** silently clobber each other's edits, with no conflict markers to warn you.
- An agent that **reviews its own work** grades its own homework — it inherits every assumption it made while writing the code.
- The work itself lives nowhere: no backlog, no statuses, no record of what was asked and what shipped.

`capataz` is a foreman for that crew. It gives agents a **file-based kanban backlog** to work from, dispatches them into **isolated git worktrees** so parallel work can't collide, routes finished work through **fresh-context reviews**, and puts a **needs-you-first dashboard** in your tmux so the moment an agent is blocked, you're one keystroke away.

## The opinions

capataz is opinionated. These are the opinions:

1. **Work is split into vertical slices** small enough for one agent to finish in one sitting, each with a predicted file footprint (`Touches:`).
2. **Parallel agents never share a working tree.** Overlapping issues run in separate waves; concurrent ones run in separate git worktrees on separate branches.
3. **Implementers never review their own work.** Reviews run in fresh sessions with no memory of how the code was written, and their findings become new backlog issues.
4. **Files are the source of truth.** The backlog is plain markdown committed in your repo — no external tracker, no database, `grep`-able and diff-able.
5. **You supervise, you don't babysit.** The dashboard sorts sessions by who needs you; hands-off work is one row with a subagent count, not a wall of terminals.

If those opinions fit how you want to work, the rest of this README shows you the machine built around them.

## The workflow

Everything runs through a pipeline of **skills** (installed into `~/.claude/skills`, so any Claude Code session can invoke them) and a **markdown backlog** in your repo:

```
 idea ──▶ plan.md ──▶ issues ──▶ ready-for-agent ──▶ parallel agents ──▶ merged
      /capataz-start   /capataz-splitter  /capataz-triage   drain (p) / single (Enter)
                                                                │
                            findings filed as new issues ◀── fresh review (R) / security review (S)
                            (back through triage — loop until a review comes back clean)
```

### The backlog

Issues are plain markdown, one feature per directory:

```
<repo>/.scratch/<feature>/issues/NN-slug.md
```

```markdown
# Add the login form

Status: ready-for-agent
Type: task
Blocked by: None
Touches: internal/tui/, main.go

## What to build
…
```

The `Status:` line is the kanban column. The lifecycle:

```
needs-triage ──▶ ready-for-agent ──▶ claimed ──▶ resolved
     │                (todo)          (doing)      (done)
     └──▶ wontfix
```

The **sidebar** shows the selected repo's backlog as a compact tracker — one colored dot per issue, sorted attention-first, with counts. The files are the source of truth: the dashboard re-reads them live, so claims and resolutions show up as agents make them. The format is compatible with mattpocock's `/to-issues` / `/triage` / `/wayfinder`, so those planning skills interoperate.

### The skills

`capataz install` ships ten skills that drive the pipeline:

**Plan** —
- **`capataz-start`** — the front door: grills a fuzzy idea into a lean plan (`.scratch/<feature>/plan.md`), then hands off to the splitter.
- **`capataz-splitter`** — breaks the plan into tracer-bullet vertical slices and publishes them as issues, exploring the codebase to predict each issue's `Touches:` so parallel-safe waves can be derived.

**Intake** —
- **`capataz-triage`** — walks `needs-triage`/`needs-info` issues, grills you for missing acceptance criteria and scope, then promotes each to `ready-for-agent` or `wontfix`. Review findings land here.
- **`capataz-investigate`** — diagnoses a bug to its root cause and files a `ready-for-agent` fix issue. It diagnoses; it does not fix.

**Build** —
- **`capataz-work`** — works one issue end to end: claim → implement → resolve.
- **`capataz-drain`** — the hands-off orchestrator behind `p` (details below).
- **`capataz-fix-conflict`** — resolves a conflicted merge-back so a drain never stalls on a `Touches:` misprediction.

**Review** —
- **`capataz-review`** — fresh-context correctness review of a feature's diff (`R`); files findings as issues.
- **`capataz-security`** — fresh-context, OWASP-lens audit of the same diff (`S`); files vulnerabilities as severity-tagged issues.

**Learn** —
- **`capataz-teach`** — explains a codebase area grounded in the real files (`file:line` citations), then quizzes you on it. For the operator, not the agents.

### Starting work on an issue

Open the issue view (`i` in the popup), pick an issue, press `Enter`. capataz dispatches it to a **new session** (or hands it to the session you have selected). Either way the receiving session acts as an **orchestrator**: it delegates the implementation to a **subagent** so its own context stays clean, and the dashboard shows the work as a `⊂N` count on that session's row.

### The hands-off drain

For a whole feature, don't babysit sessions — drain the backlog. From each issue's `Touches:` line, capataz derives which ready issues are **parallel-safe** (pairwise-disjoint files, no blocking edge) and shows it in the issue view (`⇉ parallel-safe: 01, 02`). Multi-select with `space`, press `p`, confirm — and capataz hands the drain to the managed session you're attached to, which runs `capataz-drain` as an **orchestrator** (spawning a fresh orchestrator session only if you aren't attached to one). Either way, one orchestrator works the backlog to completion on its own:

1. Scan the ready issues and take a parallel-safe wave (up to `parallel.max_concurrency`, default 3).
2. Dispatch each issue to a `capataz-work` subagent in its **own git worktree** (`~/.capataz/worktrees/…`, branch `capataz/<feature>-NN-slug`).
3. Merge each finished branch back into your base. A conflict goes to a `capataz-fix-conflict` subagent; the drain keeps going.
4. Re-scan — issues become eligible as their blockers resolve — and repeat until the backlog is drained.

You see the whole drain as **one row with a `⊂N` count**, not N sessions. `P` skips the confirm; `w` drains exactly the selected issues and stops.

> **⚠️ Parallelism belongs to the drain.** A single-issue dispatch (`Enter`) and any session you start by hand run in the **same repo working tree** — running two of those on one repo at once lets them clobber each other with no conflict markers. To parallelize, `p` a batch and let the drain isolate each agent in its own worktree. One tree, one agent.

> **Note on permissions.** A drain handed to your attached session runs under that session's normal permission posture — no new flagged process. But when capataz has to **spawn** a session with an opening prompt — the fresh reviews (`R`/`S`), a conflict fix, or a drain started while you're not attached to a managed session — it launches `claude --dangerously-skip-permissions` by default, because an unattended agent would otherwise freeze at the first interactive permission prompt with nobody there to approve it. This never applies to sessions you open by hand (`n`). Set `"dispatch": { "skip_permissions": false }` to supervise spawned sessions yourself — the tradeoff is that unattended work stalls at its first prompt.

### The review loop

Three review lanes, split by how much context they're allowed to share:

- **Per-issue sanity check** (opt-in, `review.sanity_check`): before resolving an issue, `capataz-work` spawns a fresh-context subagent to check the change against the issue's acceptance criteria — a guard against an agent fooling itself.
- **Fresh correctness review** (`R`): a **new session** running `capataz-review` — no memory of how the code was written, so it judges soundness instead of trusting the implementer. Findings are filed as `ready-for-agent` issues, so the loop continues (review → issues → fix → review) until a review comes back clean.
- **Fresh security review** (`S`): the same discipline, OWASP lens — injection, access control, secret handling, crypto, deserialization. Files each real vulnerability as a severity-tagged issue. Run it when the feature touches auth, input parsing, file/exec, network, or secrets.

Per-dispatch model routing is configurable (`models` in the config): implementation defaults to sonnet, reviews and conflict resolution to opus. `M` in the issue view overrides the model for the next dispatch.

You can also drive the backlog from the command line (this is what the skills themselves call):

```sh
capataz issue list .                       # show this repo's backlog
capataz issue new  <feature> "<title>" --body "…"   # file a new issue
capataz issue claim   .scratch/f/issues/01-x.md
capataz issue resolve .scratch/f/issues/01-x.md --answer "what shipped"
capataz issue comment .scratch/f/issues/01-x.md "a note under ## Comments"
```

## The dashboard

The dashboard is how you supervise the crew. It watches every Claude Code session in your tmux through Claude Code's hooks, sorts them **needs-you-first** (`NEEDS PERM` / `WAITING` / `ASKING` before `working`), and puts you in the blocked one in a single keystroke. Two forms:

- **`prefix + g`** — a floating **popup** switcher. Pick a session with `Enter` and it switches you there.
- **`prefix + a`** — a persistent **sidebar** docked next to your work, with live status, context-usage gauges, and the issue tracker. `Ctrl+h`/`Ctrl+l` move between the sidebar and your work.

<p align="center">
  <img src="docs/popup.png" alt="capataz popup switcher over a session" width="820">
  <br><sub>the popup switcher (<code>prefix + g</code>)</sub>
</p>

Keys inside the dashboard:

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | move selection |
| `gg` / `G` | top / bottom |
| `Ctrl+d` / `Ctrl+u` | half-page down / up |
| `Enter` / `o` | switch to the selected session (or resume it if it ended) |
| `n` | new session |
| `r` | quick reply without leaving the dashboard |
| `c` | conversation view |
| `d` | peek at the session detail without switching |
| `l` | event log |
| `R` | rename the session |
| `x` | quit the selected session (graceful, with confirm); on a resumable row — dismiss it |
| `i` | open the full-screen issue backlog for the selected repo |
| `?` | toggle keyboard shortcut help |
| `q` / `Esc` | close the dashboard |

Keys inside the **issue view** (`i`):

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | move selection |
| `Enter` / `o` | start work on the selected issue |
| `space` | toggle multi-select |
| `p` | show confirm modal then drain the selected issues hands-off |
| `P` | skip confirm and drain the whole feature immediately |
| `w` | work exactly the selected issues (bounded drain, then stop) |
| `R` | fresh-context correctness review of the feature |
| `S` | fresh-context OWASP security review of the feature |
| `M` | pick a model override for the next dispatch |
| `m` | merge resolved dispatch branches back into the base |
| `x` | prune orphaned dispatch worktrees |
| `?` | shortcuts help |
| `q` / `Esc` | return to the session list |

**Quit behavior.** When you quit a session from the dashboard (`x` → confirm `y`), the row disappears immediately and the session is hard-deleted from the daemon — it will not reappear. The graceful `/quit` signal and window teardown happen in the background. If a session's process dies *without* an operator quit — tmux-server restart, reboot, crash — the daemon retains it as a **resumable** row. Press `Enter` on a resumable row to resume the session via `claude --resume` in the original working directory.

**After a reboot**, just run `capataz` from a plain terminal: when no tmux server is running it resumes your most recently active resumable session and attaches you to it, dropping you straight back into your work. The rest of your sessions wait in the sidebar's *Resumable* panel, one `Enter` away each. Set `"resume": { "auto_attach": false }` in the config to get the new-session prompt instead.

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

`capataz install` merges hooks into `~/.claude/settings.json` (backing up the original), installs a `launchd` agent for the background daemon, installs the ten capataz skills into `~/.claude/skills`, and writes a default config. **Hooks load when a Claude session starts**, so restart any sessions you already have running.

Then bind the tmux keys (live + prints lines for your `~/.tmux.conf`):

```sh
capataz keybind          # prefix+g — summon the dashboard popup
capataz keybind attach   # prefix+a — dock a dashboard sidebar in the current window
capataz keybind nav      # Ctrl+h/j/k/l — move between panes (nvim-aware)
capataz statusline       # optional: show a "needs you" badge in your tmux status bar
```

## First run

After installing, here is the end-to-end path — from an empty dashboard to a drained feature:

1. **Open a tmux session** (or use one you already have).
2. **Bind the keys** if you have not already (see Install above).
3. **Press `prefix+g`** inside tmux. The popup switcher opens. It will be empty — that is fine.
4. **Press `n`** inside the popup to spawn a new Claude Code session. Enter a directory (Tab completes) and a name. A new tmux window opens with Claude Code and a sidebar docked to it.
5. **Interact with Claude** in that window. You will see its status update in the sidebar in real time: `working`, `waiting`, `needs you`.
6. **Switch back to the popup** (`prefix+g`) at any point. Your session appears with its current status. Press `Enter` to jump to it or `r` to quick-reply without leaving.

Then run your first feature through the pipeline:

7. **Plan it** — in a Claude session in your repo, run `/capataz-start` with a rough idea. It will interrogate you into a lean plan, then hand off to `/capataz-splitter`, which files the issues. Watch the backlog dots appear in the sidebar.
8. **Drain it** — `prefix+g`, `i` to open the issue view, `space` to select issues, `p`, confirm (or `P` to take the whole feature). The drain lands in your attached session as a `capataz-drain` orchestration — its row shows `⊂N` while implementer subagents work the backlog in parallel worktrees.
9. **Review it** — when the issues are resolved, press `R` in the issue view for a fresh-context review (and `S` for a security pass if the surface warrants it). Findings come back as new issues; triage them with `/capataz-triage` and drain again until the review is clean.

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
    "poll_seconds": 300,
    "plan_type": ""
  },
  "sidebar": { "width": 40 },
  "review": { "sanity_check": false },
  "dispatch": { "skip_permissions": true },
  "parallel": { "max_concurrency": 3 },
  "resume": { "max_retained": 20, "auto_attach": true },
  "models": {
    "implementer": "sonnet",
    "reviewer": "opus",
    "security": "opus",
    "conflict": "opus",
    "orchestrator": "default"
  },
  "context": { "limits": {} }
}
```

- `review.sanity_check` — turn on the per-issue sanity-check subagent described above; the full fresh review is always available via `R`.
- `dispatch.skip_permissions` — sessions capataz spawns with an opening prompt (fresh reviews, conflict fixes, drains started with no attached managed session) run with `--dangerously-skip-permissions` so unattended work never stalls on a prompt. It never applies to sessions you open by hand, or to a drain handed to your attached session (which keeps that session's normal permissions). Set `false` to supervise spawned sessions yourself.
- `parallel.max_concurrency` — how many issues a drain works at once.
- `resume.max_retained` — how many ended sessions are kept as resumable rows. `resume.auto_attach` — bare `capataz` resumes your most recent session when no tmux server is running (e.g. after a reboot); `false` always prompts for a new session instead.
- `models` — route a model alias (`opus` / `sonnet` / `haiku` / `fable` / `default` = inherit) to each dispatched role.
- `context.limits` — pin a context-window size per model id (e.g. `{ "claude-sonnet-4-6": "1m" }`) for the per-session context gauge.

> **Note on the usage gauge.** To show your plan usage (current-session and weekly %), `capataz` reads your Claude Code OAuth token from the macOS Keychain and calls `api/oauth/usage` — the **same endpoint Claude Code's `/usage` uses**. This is *unofficial* and undocumented: it may change or stop working at any time. It only ever reads *your* token to display *your* usage; nothing is sent anywhere else. Set `usage.enabled` to `false` to turn it off entirely, or update `url` / `keychain_service` if Anthropic changes them.
>
> Set `plan_type` to `"api"` if you're on an enterprise or API-billing plan — this replaces the session/weekly bars with a "billed per token" label. Leave blank to auto-detect from the API response.

`CAPATAZ_CONTEXT_LIMIT` (e.g. `1m`, `200k`) overrides the context-window size used for the per-session gauge. `CAPATAZ_DEBUG=1` enables verbose logging (`capataz logs`).

## Commands

```
capataz                        open the dashboard
capataz popup                  open the dashboard in a tmux popup (switcher mode)
capataz attach                 dock a dashboard sidebar in the current tmux window
capataz keybind [k]            bind prefix+k (default g) to summon the popup
capataz keybind attach [k]     bind prefix+k (default a) to attach a sidebar
capataz keybind nav            bind Ctrl+h/j/k/l pane nav (nvim-aware)
capataz statusline             show a "needs you" badge in tmux (off to remove)
capataz config                 print the config file path + contents (~/.config/capataz)
capataz issue …                claim/resolve/status/comment/list/new local .scratch issues (used by skills)
capataz install                wire hooks, install skills + start the daemon
capataz uninstall              remove hooks, skills + stop the daemon
capataz start                  start the daemon
capataz stop                   stop the daemon (no respawn)
capataz restart                restart the daemon
capataz status                 show agent + daemon state
capataz logs                   print the application log
capataz quit                   stop a running dashboard process
capataz daemon                 run the daemon in the foreground (used by launchd)
capataz version                print version
```

## Troubleshooting

**Daemon not running / dashboard shows no sessions**

```sh
capataz status           # check whether the daemon is alive
capataz logs             # print the application log
tail -f ~/.capataz/capataz.log   # follow the log live
CAPATAZ_DEBUG=1 capataz  # run the dashboard with verbose logging
```

If `capataz status` shows the daemon is stopped, run `capataz start` (or `capataz install` if you have not set it up yet). The daemon is managed by `launchd` and respawns automatically after a crash or reboot; if it stays stopped, check `capataz logs` for startup errors.

**Hooks not firing / sessions not appearing in the dashboard**

Hooks are wired into `~/.claude/settings.json` by `capataz install`. They load when a Claude session starts, not dynamically — so after running `capataz install`, you must **restart any Claude Code sessions** you already have open.

**Sidebar disappeared**

The sidebar is a Bubble Tea TUI running in a tmux pane. If it crashes (a render panic), the pane closes and the sidebar disappears. Re-attach it with `prefix+a` (`capataz keybind attach` to bind this if you have not already). If panics recur, check `capataz logs` and file a bug.

**Keybinds not working**

Run the `capataz keybind` commands from the Install section again — they apply bindings live and print the `~/.tmux.conf` lines for permanence. If you reloaded your tmux config manually, the live bindings may have been wiped; rerun them.

**launchd daemon PATH issues (tmux not found)**

The `launchd` plist bakes in your PATH at install time. If you install tmux or Homebrew after running `capataz install`, re-run `capataz install` to bake the updated PATH into the plist.

## Known limitations / beta caveats

- **macOS only.** `capataz` uses the macOS Keychain (`security`) and `launchd`. It does not run on Linux or Windows.
- **tmux only.** Only Claude Code sessions running inside tmux panes are tracked. Sessions in bare terminals or other multiplexers are invisible to the daemon.
- **Spawned dispatch sessions run with permissions pre-granted.** Work handed to your attached session keeps that session's normal permissions, but sessions capataz spawns with an opening prompt — fresh reviews (`R`/`S`), conflict fixes, drains started with no attached managed session — run `claude --dangerously-skip-permissions` by default so unattended work doesn't stall, meaning those agents can run tools without asking. Set `"dispatch": { "skip_permissions": false }` to supervise them yourself.
- **Usage gauge is unofficial.** The plan usage bars read your OAuth token from the Keychain and call the same internal endpoint Claude Code's `/usage` uses. This is undocumented and may break without notice. You can disable it with `"usage": { "enabled": false }` in your config, or update `url` / `keychain_service` if Anthropic changes them.
- **Early beta.** Rough edges exist. If something breaks, `capataz logs` and `CAPATAZ_DEBUG=1` are your friends.

## Uninstall

```sh
capataz uninstall   # removes hooks + the launchd daemon
```

## License

[MIT](LICENSE)
