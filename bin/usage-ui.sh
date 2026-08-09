#!/bin/sh
# Plan-usage + context pane hosted in a herdr popup.
#
# Runs ./bin/usage-view, which renders two things and refreshes every 2s until
# you press q:
#
#   Plan usage — `capataz usage` output (Keychain token; unofficial Anthropic
#                endpoint — see capataz docs). Optional: a missing capataz
#                degrades to a dim note, it does not fail the pane.
#   Context    — one gauge per herdr agent pane, read straight from each
#                agent's Claude Code transcript (located via the session id in
#                `herdr agent list`). No capataz daemon involved, which matters
#                because `capataz install --herdr` installs no daemon.
#
# Invoked through $HERDR_PLUGIN_ROOT — a bare relative path would resolve
# against the pane's --cwd (the focused repo dir), not the plugin root. The
# pane's cwd is still meaningful: usage-view uses it to mark the row for the
# pane you opened this from.
set -eu

root="${HERDR_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"
view="$root/bin/usage-view"

if [ ! -x "$view" ]; then
  printf 'usage-view not built — run: sh %s/scripts/build.sh\n' "$root"
  # Fall back to the plain plan gauge so the pane still shows something useful.
  bin="${CAPATAZ_BIN:-capataz}"
  if command -v "$bin" >/dev/null 2>&1 || [ -x "$bin" ]; then
    printf '\n'; "$bin" usage || true
  fi
  printf '\npress any key to close…'; read -r _; exit 1
fi

exec "$view"
