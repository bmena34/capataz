#!/bin/sh
# Plan-usage pane hosted in a herdr overlay.
#
# Runs `capataz usage` to print the live plan-usage gauge (reads the Claude
# Code OAuth token from the macOS Keychain; calls an unofficial Anthropic
# endpoint — see capataz docs). Output is plain text; exits immediately.
# CAPATAZ_BIN lets a locally-built binary be used before `make install`.
#
# Opened via the `usage` pane entrypoint or the `usage` action.
# Invoked through $HERDR_PLUGIN_ROOT — a bare relative path would resolve
# against the pane's --cwd (the focused repo dir), not the plugin root.
set -eu

bin="${CAPATAZ_BIN:-capataz}"
if ! command -v "$bin" >/dev/null 2>&1 && [ ! -x "$bin" ]; then
  printf 'capataz not found (%s) — build/install it first\n' "$bin"
  printf '  make build && sudo make install\n'
  printf '  or: export CAPATAZ_BIN=/path/to/capataz\n'
  printf 'press any key to close…'; read -r _; exit 1
fi

"$bin" usage
printf '\npress any key to close…'; read -r _
