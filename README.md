# capataz-herdr

A [herdr](https://herdr.dev) plugin that brings capataz's workflow layer into herdr as a first-class plugin.

## The two-binary story

**herdr** is the AI coding agent substrate — it hosts panes, manages worktrees, routes agent state, and provides the plugin API. It is a better foundation for multi-agent coding workflows than capataz's hand-rolled tmux wrapper: Rust-native, cross-platform, multi-workspace, with session persistence and a real plugin API.

**capataz** is the workflow layer — the `.scratch` issue backlog, `capataz issue` CLI, `ParallelSafe` dispatch waves, the drain orchestrator, and the shipped skills. None of this exists in herdr or any existing plugin (including `cloudmanic/herdr-plus`).

This plugin wires the two together: herdr runs the substrate; capataz provides the planning and orchestration logic above it.

## What this plugin provides

- **Backlog popup** — `capataz issues` rendered inside a herdr-managed popup pane (~70% of the terminal), scoped to the focused pane's repo (reads the repo's `.scratch` backlog). Interactive: cursor movement, status badges, feature grouping. Dismissed with `q`.
- **Usage & context popup** — a live popup (refreshes every 2s, `q` to close) with two sections: the **plan-usage gauge** from `capataz usage`, and a **context gauge per herdr agent pane** showing how full each agent's context window is. Context is read straight from each agent's Claude Code transcript — located exactly via the session id herdr reports in `herdr agent list` — so it needs **no capataz daemon** (which `capataz install --herdr` deliberately doesn't install). See [Context gauges](#context-gauges).
- **Open-backlog / usage actions** — keyboard-bindable herdr actions that open those popups in the focused pane's repo cwd.
- **Drain action** — invokes the dispatch orchestrator (`cmd/drain`): scans the feature backlog, takes a parallel-safe wave of `ready-for-agent` issues, and for each spins up a git worktree, dispatches a fresh `capataz-work` **agent** into it (default posture — no bypass-permissions), waits for it to finish, verifies it committed, and merges the branch back (resolving conflicts with a `capataz-fix-conflict` agent when needed). Loops until the backlog drains.
- **`worktree.created` event hook** — herdr fires this when it creates a git worktree; the drain loop uses it to coordinate dispatch.

## Requirements

- [herdr](https://herdr.dev) >= 0.8.0
- **herdr's Claude integration installed** — `herdr integration install claude`. This is the hook that lets herdr detect Claude agent state; the drain's `herdr agent wait --until done` depends on it. **Without it the drain hangs.**
- [capataz](https://github.com/bmena34/capataz) on PATH (or set `CAPATAZ_BIN`), built from a version that has the `issues`, `issue waves`, `usage`, and `trust-dir` subcommands (the `herdr-port` branch / once merged, `main`). The plugin shells out to all four. (`usage` is only needed for the plan line of the usage popup — the context gauges work without capataz installed at all.) After building the binary, run **`capataz install --herdr`** once — the lean, herdr-only install that lays down just the skills + agents + default config the dispatched agents need (**no** capataz hooks, **no** launchd daemon; herdr owns the substrate). Plain `capataz install` also works but drags in capataz's own tmux/launchd machinery you don't need under herdr.
- Go >= 1.22 (to build the drain orchestrator — see Build below)

## Operator runbook

### 1. Link the plugin

```sh
# from the capataz-herdr repo root:
herdr plugin link .

# or by absolute path from anywhere:
herdr plugin link /path/to/capataz-herdr

# pick up changes:
herdr server reload-config
```

### 2. Build the binaries

The drain action requires `./bin/drain` and the usage popup requires
`./bin/usage-view`. Build both with:

```sh
# from the capataz-herdr repo root:
sh scripts/build.sh
```

`herdr plugin install` runs this automatically. For `herdr plugin link .` (local dev), run the script once manually after cloning.

### 3. Verify the plugin registered

```sh
herdr plugin list
# should show: bmena34.capataz-herdr  enabled
```

### 4. Bind the actions in herdr's config.toml (`~/.config/herdr/config.toml`)

A plugin-action bind is a `[[keys.command]]` entry with `type = "plugin_action"`
and a single `command` of the form `<plugin-id>.<action-id>`. Trigger it with the
herdr prefix (default `ctrl+b`) then the key — e.g. `ctrl+b` then `i`.

```toml
# open the backlog popup in the focused pane's repo  →  ctrl+b i
[[keys.command]]
key = "prefix+i"
type = "plugin_action"
command = "bmena34.capataz-herdr.issues"
description = "capataz: open backlog"

# open the usage + context popup  →  ctrl+b u
[[keys.command]]
key = "prefix+u"
type = "plugin_action"
command = "bmena34.capataz-herdr.usage"
description = "capataz: usage & context"
```

Then `herdr server reload-config` (or restart) to apply, and `herdr config check`
to validate.

> The **`drain`** action is intentionally *not* keybound: it needs a feature slug
> and `--repo`, which a bare action can't supply. Run it from a shell instead —
> see [Run a drain](#6-run-a-drain).

### 5. Open the backlog popup

From any herdr pane inside a repo that has a `.scratch` backlog:

```sh
# via keybind (configured above), or from the CLI:
herdr plugin action invoke issues
```

The popup opens in the focused pane's repo cwd. Navigate with `j`/`k`, dismiss with `q`.

### Context gauges

The `usage` popup shows one gauge per herdr agent pane:

```
Plan: Enterprise / API (billed per token — no rate-limit bars)

Context
▸ capataz-herdr        51k / 1.0M   [█░░░░░░░░░]   5%
  drain-03-usage-pane  148k / 200k  [███████▌░░]  74%

q quit · refresh 2s
```

`▸` marks the pane you opened the popup from. Rows refresh every 2s; the plan
line refreshes every minute (it makes a network call). Press `q`, `Esc` or
`Ctrl-C` to close.

**How it works.** `herdr agent list` reports each agent pane's session id, which
names that agent's Claude Code transcript under `~/.claude/projects/`. The view
tail-reads each transcript and takes the newest assistant turn's
`input + cache_read + cache_creation` tokens — the same arithmetic capataz's own
gauge uses, but daemon-free, since `capataz install --herdr` installs no daemon.
Subagent (`isSidechain`) turns are skipped, so a session that just dispatched a
subagent doesn't appear to have emptied out.

**Setting the context window for 1M sessions.** Transcripts record a bare model
id (`claude-opus-5`) with no `[1m]` marker, so a model offered in both 200k and
1M forms is indistinguishable — the view assumes 200k. If you run a 1M variant,
pin it in `~/.config/capataz/config.json` (the same key capataz's own gauge
reads, so one setting serves both):

```json
{ "context": { "limits": { "claude-opus-5": "1m" } } }
```

`CAPATAZ_CONTEXT_LIMIT=1m` overrides every session for one run. As a safety net,
a session measured above its assumed window widens automatically rather than
rendering an impossible >100% bar.

**Not a Claude agent?** Panes running another agent still appear, annotated
(`codex — no context data`) rather than silently dropped.

### 6. Run a drain

The drain orchestrator takes a feature slug and the repo it lives in, so run the
binary directly (the bound `drain` action is a convenience wrapper; the binary is
the source of truth):

```sh
# concurrency N runs up to N parallel-safe issues at once; default 3.
./bin/drain --repo /path/to/repo <feature>
./bin/drain --repo /path/to/repo --concurrency 2 <feature>

# preview without doing anything:
./bin/drain --dry-run --repo /path/to/repo <feature>
```

For each `ready-for-agent` issue it: creates a git worktree, **pre-trusts it**
(`capataz trust-dir`, so the agent skips Claude's folder-trust prompt), starts a
fresh `capataz-work` **agent** in default posture (`--allowedTools`, no bypass
screen — a permission prompt surfaces as a `blocked` agent), waits for it to
finish, **verifies it actually committed**, then merges the branch back
(serialized so concurrent merges don't race). A conflicting merge-back spins up a
`capataz-fix-conflict` agent in the base repo, and the drain verifies the merge
actually completed before moving on. It re-scans and loops until the backlog is
drained, cleaning up worktrees and merged branches as it goes.

Flags: `--skip-perms` opts into `--dangerously-skip-permissions` instead of the
default allowlist (needs the one-time bypass-permissions screen accepted first).
`CAPATAZ_BIN` overrides the `capataz` binary path.

### Invoke actions from the CLI

```sh
# open the backlog popup in the focused pane's repo
herdr plugin action invoke issues

# run the drain orchestrator
herdr plugin action invoke drain
```

Note: the action id is the **positional arg**; `--plugin` is an optional trailing flag. The `--plugin=` equals form is broken in herdr 0.8.0 — use the space-separated form.

### Override the capataz binary (before `make install`)

The plugin shells out to `capataz` on PATH. To use a locally-built binary before running `make install` in the capataz repo:

```sh
export CAPATAZ_BIN=/path/to/capataz/capataz
herdr plugin action invoke issues
# or:
herdr plugin action invoke drain
```

### Unlink

```sh
herdr plugin unlink bmena34.capataz-herdr
# or: herdr plugin disable bmena34.capataz-herdr
```

## Port mapping: what herdr owns vs what capataz owns

| capataz piece | fate in this plugin |
|---|---|
| `internal/issues`, `internal/worktree`, `skills/` | port ~as-is (daemon-free already); `capataz issue` CLI is the source of truth |
| issue view (`internal/tui issueview.go`) | repackaged as the `[[panes]] issues` popup (real Bubble Tea, not a plain list) |
| dashboard / sidebar | dropped — herdr renders the agent panel |
| `tmux.go` | rewritten → herdr socket CLI (`herdr worktree create`, `herdr agent prompt`) |
| hook → daemon → dashboard broadcast | dropped entirely — herdr's own Claude integration (`herdr integration install claude`) provides agent state; capataz doesn't feed or override it |
| usage gauge | retained as the `usage` popup pane (`capataz usage`); herdr has no equivalent |
| context gauge (daemon-fed, `internal/daemon`) | reimplemented daemon-free in `cmd/usage`: reads transcripts directly, keyed by the session id in `herdr agent list` |
| launchd daemon + SQLite persistence | dropped — herdr owns persistence |
| `capataz-drain` skill (orchestrator logic) | replaced by `cmd/drain` Go binary (this repo's dispatch engine) |

The dispatch layer — the Go binary at `cmd/drain` — does what `capataz-drain` did inside capataz's managed tmux sessions, but driven by herdr's worktree and pane APIs instead.

## Project structure

```
herdr-plugin.toml   — plugin manifest (build step, panes, actions, events)
go.mod              — module github.com/bmena34/capataz-herdr
scripts/
  build.sh          — builds ./bin/drain and ./bin/usage-view (run once after link)
cmd/
  drain/            — Go source for the dispatch orchestrator
  usage/            — Go source for the usage + context popup
bin/
  issues-ui.sh      — pane entrypoint: runs `capataz issues` in the pane cwd
  open-issues.sh    — action: opens the issues popup in the focused repo
  usage-ui.sh       — pane entrypoint: runs ./bin/usage-view
  open-usage.sh     — action: opens the usage popup in the focused repo
  on-worktree.sh    — event hook: logs worktree.created events
  drain             — built orchestrator binary (gitignored, produced by build.sh)
  usage-view        — built usage/context view (gitignored, produced by build.sh)
```

`usage-view` also runs standalone, outside herdr:

```sh
./bin/usage-view              # live view (2s refresh, q to quit)
./bin/usage-view -once        # print one frame and exit
./bin/usage-view -no-plan     # context only, skip the plan gauge
./bin/usage-view -limit 1m    # override the assumed context window
```

## Status

The hands-off drain is validated end to end against real Claude agents — both the
**clean path** (worktrees → agents → verified merges) and the **conflict path** (a
real merge conflict resolved by a `capataz-fix-conflict` agent in the base repo,
verified complete). Default posture, no bypass-permissions mode.

- **Publishing to `herdr plugin install` is deferred.** This repo is private, so
  `herdr plugin install bmena34/capataz-herdr` won't work yet — use `herdr plugin
  link .` from a local checkout. It'll work once the repo is made public.

## Proven gotchas (herdr 0.8.0)

1. **Pane entrypoint paths resolve against the pane's `--cwd`, NOT the plugin root.** Since the pane opens in an arbitrary repo dir, `command = ["./bin/issues-ui.sh"]` fails (looks in the repo). Fix: `command = ["sh","-c","exec \"$HERDR_PLUGIN_ROOT/bin/issues-ui.sh\""]`.

2. **Actions receive the focused pane's cwd via `HERDR_PLUGIN_CONTEXT_JSON`** (a JSON blob with `focused_pane_cwd` / `workspace_cwd`), not a plain env var and not the action's own `PWD` (which is the plugin root). `open-issues.sh` parses `focused_pane_cwd` from it.
