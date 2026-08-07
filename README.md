# capataz-herdr

A [herdr](https://herdr.io) plugin that brings capataz's workflow layer into herdr as a first-class plugin.

## The two-binary story

**herdr** is the AI coding agent substrate — it hosts panes, manages worktrees, routes agent state, and provides the plugin API. It is the better foundation for a multi-agent coding workflow than capataz's hand-rolled tmux wrapper.

**capataz** is the workflow layer — the `.scratch` issue backlog, `capataz issue` CLI, `ParallelSafe` dispatch waves, the drain orchestrator, and the shipped skills. None of this exists in herdr or any existing plugin (including `cloudmanic/herdr-plus`).

This plugin wires the two together: herdr runs the substrate; capataz provides the planning and orchestration logic above it.

## What this plugin provides

- **Backlog overlay** — `capataz issues` rendered inside a herdr-managed overlay pane, scoped to the focused pane's repo (reads the repo's `.scratch` backlog). Interactive: cursor movement, status badges, feature grouping. Dismissed with `q`.
- **Open-backlog action** — keyboard-bindable herdr action that opens the overlay in the currently-focused pane's repo cwd.
- **Drain action** — invokes the dispatch orchestrator (implemented in issue 03): scans the feature backlog, takes a parallel-safe wave of ready issues, dispatches `capataz-work` subagents into herdr worktrees, and merges back.
- **`worktree.created` event hook** — herdr fires this when it creates a git worktree; the drain loop uses it to coordinate dispatch.

## Requirements

- [herdr](https://herdr.io) >= 0.8.0
- [capataz](https://github.com/bmena34/capataz) installed and on PATH (or set `CAPATAZ_BIN`)

## Local development runbook

### Link the plugin

```sh
# from the capataz-herdr repo root
herdr plugin link .

# or by absolute path from anywhere:
herdr plugin link /Users/brandon/code/capataz-herdr

# pick up changes:
herdr server reload-config
```

### Verify it registered

```sh
herdr plugin list
# should show: bmena34.capataz-herdr  enabled
```

### Bind the actions

In herdr's `config.toml`, add keybinds with `type = "plugin_action"`:

```toml
# Example: open the backlog overlay
[[keybinds]]
key = "ctrl+shift+i"
type = "plugin_action"
plugin = "bmena34.capataz-herdr"
action = "issues"
```

### Invoke actions from the CLI

```sh
# open the backlog overlay in the focused pane's repo
herdr plugin action invoke issues

# run the drain orchestrator (available after issue 03)
herdr plugin action invoke drain
```

Note: the action id is the **positional arg**; `--plugin` is an optional trailing flag. The `--plugin=` equals form is broken in herdr 0.8.0 — use the space-separated form.

### Override the capataz binary (before `make install`)

```sh
export CAPATAZ_BIN=/Users/brandon/code/capataz/capataz
herdr plugin action invoke issues
```

### Unlink

```sh
herdr plugin unlink bmena34.capataz-herdr
# or: herdr plugin disable bmena34.capataz-herdr
```

## Project structure

```
herdr-plugin.toml   — full plugin manifest (panes, actions, events)
go.mod              — module github.com/bmena34/capataz-herdr
bin/
  issues-ui.sh      — pane entrypoint: runs `capataz issues` in the pane cwd
  open-issues.sh    — action: opens the issues overlay in the focused repo
  on-worktree.sh    — event hook: logs worktree.created events (drain dispatch in issue 03)
  drain             — built orchestrator binary (issue 03, gitignored until built)
```

## What's coming (later issues)

- **Issue 03** — the `cmd/drain` orchestrator binary: scans backlog, dispatches `capataz-work` subagents via `herdr worktree`, merges resolved branches back.
- **Packaging** — `herdr plugin publish` / registry submission.

## Proven gotchas (herdr 0.8.0)

1. **Pane entrypoint paths resolve against the pane's `--cwd`, NOT the plugin root.** Since the pane opens in an arbitrary repo dir, `command = ["./bin/issues-ui.sh"]` fails (looks in the repo). Fix: `command = ["sh","-c","exec \"$HERDR_PLUGIN_ROOT/bin/issues-ui.sh\""]`.

2. **Actions receive the focused pane's cwd via `HERDR_PLUGIN_CONTEXT_JSON`** (a JSON blob with `focused_pane_cwd` / `workspace_cwd`), not a plain env var, and not the action's own `PWD` (which is the plugin root). `open-issues.sh` parses `focused_pane_cwd` from it.
