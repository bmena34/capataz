#!/bin/sh
# Action entry point: open the `usage` overlay pane.
#
# herdr runs actions non-interactively; focused-pane context (including cwd)
# arrives as JSON in HERDR_PLUGIN_CONTEXT_JSON. The usage command is not
# repo-scoped (it reads from the Keychain), so cwd is used only to satisfy
# herdr's pane --cwd parameter.
#
# Usage (from herdr keybind or CLI):
#   herdr plugin action invoke usage
set -eu

ctx="${HERDR_PLUGIN_CONTEXT_JSON:-}"
# Prefer the focused pane's cwd, fall back to workspace cwd, then PWD.
cwd=$(printf '%s' "$ctx" | sed -n 's/.*"focused_pane_cwd":"\([^"]*\)".*/\1/p')
[ -n "$cwd" ] || cwd=$(printf '%s' "$ctx" | sed -n 's/.*"workspace_cwd":"\([^"]*\)".*/\1/p')
[ -n "$cwd" ] || cwd="$PWD"

exec herdr plugin pane open \
  --plugin bmena34.capataz-herdr \
  --entrypoint usage \
  --placement overlay \
  --cwd "$cwd" \
  --env "CAPATAZ_BIN=${CAPATAZ_BIN:-capataz}" \
  --focus
