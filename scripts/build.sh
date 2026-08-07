#!/bin/sh
#
# Build the capataz-herdr drain orchestrator for `herdr plugin install`.
# herdr runs this as the manifest's [[build]] step after cloning the repo,
# in the plugin root.
#
# This repo requires Go (unlike herdr-plus, there is no prebuilt-release
# fallback — the drain orchestrator is tightly coupled to the local
# capataz binary and is not published to any release registry yet).
#
# Result: ./bin/drain  (gitignored; the drain action in herdr-plugin.toml
# points at it).

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
