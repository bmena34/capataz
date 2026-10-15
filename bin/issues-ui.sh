#!/bin/sh
# Backlog view hosted in a herdr popup pane: `capataz issues` scoped to the
# pane's cwd (the repo herdr opened the pane in).
#
# Invoked via $HERDR_PLUGIN_ROOT — see the [[panes]] command in
# herdr-plugin.toml for why a relative path would fail.
set -eu

root="${HERDR_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"
bin="$root/bin/capataz"

if [ ! -x "$bin" ]; then
  printf 'capataz not built — run: sh %s/scripts/build.sh\n' "$root"
  printf 'press any key to close…'; read -r _; exit 1
fi

exec "$bin" issues "$PWD"
