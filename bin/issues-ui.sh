#!/bin/sh
# Backlog view hosted in a herdr overlay pane.
#
# Runs capataz's daemon-free full-screen issue viewer (`capataz issues`) scoped
# to the pane's cwd (the repo herdr opened the pane in). CAPATAZ_BIN lets a
# locally-built binary be used before `make install`.
#
# This script is invoked via $HERDR_PLUGIN_ROOT — see the [[panes]] command in
# herdr-plugin.toml for why a relative path would fail.
set -eu

bin="${CAPATAZ_BIN:-capataz}"
if ! command -v "$bin" >/dev/null 2>&1 && [ ! -x "$bin" ]; then
  printf 'capataz not found (%s) — build/install it first\n' "$bin"
  printf '  make build && sudo make install\n'
  printf '  or: export CAPATAZ_BIN=/path/to/capataz\n'
  printf 'press any key to close…'; read -r _; exit 1
fi

exec "$bin" issues "$PWD"
