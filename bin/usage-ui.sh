#!/bin/sh
# Plan-usage + context pane hosted in a herdr popup: `capataz usage-view`,
# refreshing every 2s until q.
#
# Invoked through $HERDR_PLUGIN_ROOT — a bare relative path would resolve
# against the pane's --cwd (the focused repo dir), not the plugin root. The
# pane's cwd still matters: the view uses it to mark the row for the pane you
# opened this from.
set -eu

root="${HERDR_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"
bin="$root/bin/capataz"

if [ ! -x "$bin" ]; then
  printf 'capataz not built — run: sh %s/scripts/build.sh\n' "$root"
  printf 'press any key to close…'; read -r _; exit 1
fi

exec "$bin" usage-view
