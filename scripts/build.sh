#!/bin/sh
#
# Build the capataz-herdr binaries for `herdr plugin install`. herdr runs this
# as the manifest's [[build]] step after cloning the repo, in the plugin root.
#
# This repo requires Go (unlike herdr-plus, there is no prebuilt-release
# fallback — the drain orchestrator is tightly coupled to the local
# capataz binary and is not published to any release registry yet).
#
# Results (both gitignored; herdr-plugin.toml points at them):
#   ./bin/drain       the drain orchestrator (drain action)
#   ./bin/usage-view  the plan-usage + context popup (usage pane)

set -eu

mkdir -p bin

if ! command -v go >/dev/null 2>&1; then
    echo "capataz-herdr: Go toolchain not found — cannot build ./bin/drain." >&2
    echo "Install Go (https://go.dev/dl/) and re-run: sh scripts/build.sh" >&2
    exit 1
fi

echo "capataz-herdr: building drain orchestrator (go build ./cmd/drain)..." >&2
go build -o bin/drain ./cmd/drain
echo "capataz-herdr: built ./bin/drain" >&2

echo "capataz-herdr: building usage view (go build ./cmd/usage)..." >&2
go build -o bin/usage-view ./cmd/usage
echo "capataz-herdr: built ./bin/usage-view" >&2
