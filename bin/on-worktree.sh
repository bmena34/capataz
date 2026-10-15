#!/bin/sh
# Event hook: herdr fired worktree.created.
#
# herdr passes the event payload as JSON in HERDR_PLUGIN_EVENT_JSON.
# This handler logs the event. In the drain loop (issue 03), this is where
# capataz-drain dispatches a capataz-work subagent into the new worktree.
set -eu

log="${HERDR_PLUGIN_STATE_DIR:-/tmp}/capataz-worktree-events.log"
printf '%s worktree.created: %s\n' \
  "$(date -u +%FT%TZ)" \
  "${HERDR_PLUGIN_EVENT_JSON:-<no HERDR_PLUGIN_EVENT_JSON>}" \
  >>"$log"
printf 'logged worktree.created to %s\n' "$log"
