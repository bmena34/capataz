# capataz-herdr

A [herdr](https://herdr.io) plugin that brings capataz's workflow layer into herdr as a first-class plugin.

## The two-binary story

**herdr** is the AI coding agent substrate — it hosts panes, manages worktrees, routes agent state, and provides the plugin API. It is a better foundation for multi-agent coding workflows than capataz's hand-rolled tmux wrapper: Rust-native, cross-platform, multi-workspace, with session persistence and a real plugin API.

**capataz** is the workflow layer — the `.scratch` issue backlog, `capataz issue` CLI, `ParallelSafe` dispatch waves, the drain orchestrator, and the shipped skills. None of this exists in herdr or any existing plugin (including `cloudmanic/herdr-plus`).

This plugin wires the two together: herdr runs the substrate; capataz provides the planning and orchestration logic above it.

## What this plugin provides

- **Backlog overlay** — `capataz issues` rendered inside a herdr-managed overlay pane, scoped to the focused pane's repo (reads the repo's `.scratch` backlog). Interactive: cursor movement, status badges, feature grouping. Dismissed with `q`.
- **Open-backlog action** — keyboard-bindable herdr action that opens the overlay in the currently-focused pane's repo cwd.
- **Drain action** — invokes the dispatch orchestrator (`cmd/drain`): scans the feature backlog, takes a parallel-safe wave of ready issues, dispatches `capataz-work` subagents into herdr worktrees, and merges back.
- **`worktree.created` event hook** — herdr fires this when it creates a git worktree; the drain loop uses it to coordinate dispatch.

## Requirements

- [herdr](https://herdr.io) >= 0.8.0
- [capataz](https://github.com/bmena34/capataz) installed and on PATH (or set `CAPATAZ_BIN`)
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

### 2. Build the drain orchestrator

The drain action requires a compiled binary at `./bin/drain`. Build it with:

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

### 4. Bind the actions in herdr's config.toml

Add keybinds with `type = "plugin_action"` in your herdr `config.toml`:

```toml
# open the backlog overlay in the focused pane's repo
[[keybinds]]
key = "ctrl+shift+i"
type = "plugin_action"
plugin = "bmena34.capataz-herdr"
action = "issues"

# run the drain orchestrator (hands-off, dispatches capataz-work subagents)
[[keybinds]]
key = "ctrl+shift+d"
type = "plugin_action"
plugin = "bmena34.capataz-herdr"
action = "drain"
```

### 5. Open the backlog overlay

From any herdr pane inside a repo that has a `.scratch` backlog:

```sh
# via keybind (configured above), or from the CLI:
herdr plugin action invoke issues
```

The overlay opens in the focused pane's repo cwd. Navigate with `j`/`k`, dismiss with `q`.

### 6. Run a drain

```sh
# via keybind (configured above), or from the CLI:
herdr plugin action invoke drain
```

The drain orchestrator scans the feature backlog, picks a parallel-safe wave of `ready-for-agent` issues, dispatches `capataz-work` subagents into herdr worktrees (one per issue), and merges each resolved branch back. It loops until the backlog is drained.

### Invoke actions from the CLI

```sh
# open the backlog overlay in the focused pane's repo
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
| issue view (`internal/tui issueview.go`) | repackaged as the `[[panes]] issues` overlay (real Bubble Tea, not a plain list) |
| dashboard / sidebar | dropped — herdr renders the agent panel |
| `tmux.go` | rewritten → herdr socket CLI (`herdr worktree create`, `herdr agent prompt`) |
| hook → daemon → dashboard broadcast | simplified → hook calls `herdr pane report-agent`; broadcast half dropped |
| usage gauge | retained as a future pane/statusline (herdr has no equivalent today) |
| launchd daemon + SQLite persistence | dropped — herdr owns persistence |
| `capataz-drain` skill (orchestrator logic) | replaced by `cmd/drain` Go binary (this repo's dispatch engine) |

The dispatch layer — the Go binary at `cmd/drain` — does what `capataz-drain` did inside capataz's managed tmux sessions, but driven by herdr's worktree and pane APIs instead.

## Project structure

```
herdr-plugin.toml   — plugin manifest (build step, panes, actions, events)
go.mod              — module github.com/bmena34/capataz-herdr
scripts/
  build.sh          — builds ./bin/drain from ./cmd/drain (run once after link)
cmd/
  drain/            — Go source for the dispatch orchestrator
bin/
  issues-ui.sh      — pane entrypoint: runs `capataz issues` in the pane cwd
  open-issues.sh    — action: opens the issues overlay in the focused repo
  on-worktree.sh    — event hook: logs worktree.created events
  drain             — built orchestrator binary (gitignored, produced by build.sh)
```

## Status / not yet done

- **Conflict path needs a live shakeout.** When a drain subagent's branch conflicts on merge-back, the orchestrator falls back to a manual-resolution path. This has not yet been exercised end-to-end against a real conflict; hardening is tracked as a needs-triage issue.
- **Publishing to `herdr plugin install` is deferred.** `herdr plugin install bmena34/capataz-herdr` will work once the repo is public. For now use `herdr plugin link .` from a local checkout.

## Proven gotchas (herdr 0.8.0)

1. **Pane entrypoint paths resolve against the pane's `--cwd`, NOT the plugin root.** Since the pane opens in an arbitrary repo dir, `command = ["./bin/issues-ui.sh"]` fails (looks in the repo). Fix: `command = ["sh","-c","exec \"$HERDR_PLUGIN_ROOT/bin/issues-ui.sh\""]`.

2. **Actions receive the focused pane's cwd via `HERDR_PLUGIN_CONTEXT_JSON`** (a JSON blob with `focused_pane_cwd` / `workspace_cwd`), not a plain env var and not the action's own `PWD` (which is the plugin root). `open-issues.sh` parses `focused_pane_cwd` from it.
