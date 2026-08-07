#!/bin/sh
# Action entry point: open the `issues` overlay pane in the focused pane's repo.
#
# herdr runs actions non-interactively and passes context (including the focused
# pane's cwd) as JSON in HERDR_PLUGIN_CONTEXT_JSON — NOT as a plain env var and
# NOT via the action's own PWD (which is the plugin root). Parse the repo cwd
# out of that JSON and hand it to the pane open command.
#
# Usage (from herdr keybind or CLI):
#   herdr plugin action invoke issues
set -eu

ctx="${HERDR_PLUGIN_CONTEXT_JSON:-}"
# Prefer the focused pane's cwd, fall back to workspace cwd, then PWD.
cwd=$(printf '%s' "$ctx" | sed -n 's/.*"focused_pane_cwd":"\([^"]*\)".*/\1/p')
[ -n "$cwd" ] || cwd=$(printf '%s' "$ctx" | sed -n 's/.*"workspace_cwd":"\([^"]*\)".*/\1/p')
[ -n "$cwd" ] || cwd="$PWD"

exec herdr plugin pane open \
  --plugin bmena34.capataz-herdr \
  --entrypoint issues \
  --placement overlay \
  --cwd "$cwd" \
  --env "CAPATAZ_BIN=${CAPATAZ_BIN:-capataz}" \
  --focus
