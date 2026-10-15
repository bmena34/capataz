#!/bin/sh
# Action entry point: open the `usage` (plan usage + context) popup pane.
#
# herdr runs actions non-interactively; focused-pane context (including cwd)
# arrives as JSON in HERDR_PLUGIN_CONTEXT_JSON. The plan gauge is not
# repo-scoped (it reads from the Keychain), but the cwd still matters: the view
# marks the row for the pane you opened this from by matching that cwd against
# each agent's. That match is what identifies the originating pane, since
# opening this popup with --focus can take herdr's own `focused` flag off it.
#
# Usage (from herdr keybind or CLI):
#   herdr plugin action invoke usage
set -eu

ctx="${HERDR_PLUGIN_CONTEXT_JSON:-}"
# Prefer the focused pane's cwd, fall back to workspace cwd, then PWD.
cwd=$(printf '%s' "$ctx" | sed -n 's/.*"focused_pane_cwd":"\([^"]*\)".*/\1/p')
[ -n "$cwd" ] || cwd=$(printf '%s' "$ctx" | sed -n 's/.*"workspace_cwd":"\([^"]*\)".*/\1/p')
[ -n "$cwd" ] || cwd="$PWD"

# Placement/size (a minimal fixed-cell popup) come from the [[panes]] usage
# declaration in herdr-plugin.toml — omit --placement/--width/--height here so
# that manifest stays the single source of truth (a --placement flag overrides it).
exec herdr plugin pane open \
  --plugin bmena34.capataz \
  --entrypoint usage \
  --cwd "$cwd" \
  --focus
