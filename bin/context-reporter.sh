#!/bin/sh
# Startup hook: launch the sidebar context reporter as a single-instance daemon.
#
# The reporter (usage-view -report) polls each agent pane's Claude Code
# transcript and pushes a compact context gauge back into herdr as the
# pane-metadata token "context" (herdr pane report-metadata --token). Sidebar
# agent rows render it with the "$context" token — see README "Sidebar context
# gauges" for the config.toml rows.
#
# herdr startup hooks must exit (they run once per server start and again on
# live handoff), so this backgrounds the reporter behind a pidfile: a repeat
# invocation that finds the daemon alive does nothing, a dead one is replaced.
# Tokens carry a TTL, so if the daemon dies its gauges expire off the sidebar
# rather than freezing; the daemon itself exits after ~1 minute of herdr being
# unreachable and is relaunched by the next server's startup hook.
#
# Usage (herdr invokes this via the manifest's [[startup]]; run by hand to
# start it without restarting herdr):
#   ./bin/context-reporter.sh
set -eu

# Startup hooks get HERDR_PLUGIN_ROOT; fall back to this script's parent so the
# reporter can also be started straight from a shell in the repo.
root="${HERDR_PLUGIN_ROOT:-$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)}"

state="${XDG_STATE_HOME:-$HOME/.local/state}/capataz-herdr"
mkdir -p "$state"
pidfile="$state/context-reporter.pid"

if [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
  exit 0
fi

# 5s is plenty for a sidebar gauge and keeps the herdr shell-outs cheap; the
# reporter only logs sweep failures, so the log stays small.
nohup "$root/bin/usage-view" -report -interval 5s \
  >>"$state/context-reporter.log" 2>&1 &
echo $! >"$pidfile"
