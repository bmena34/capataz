#!/bin/sh
#
# Build ./bin/capataz for the herdr plugin. herdr runs this as the manifest's
# [[build]] step after `herdr plugin install`, in the plugin root; after
# `herdr plugin link`, run it by hand. Requires Go.
set -eu

mkdir -p bin

if ! command -v go >/dev/null 2>&1; then
    echo "capataz: Go toolchain not found — cannot build ./bin/capataz." >&2
    echo "Install Go (https://go.dev/dl/) and re-run: sh scripts/build.sh" >&2
    exit 1
fi

go build -o bin/capataz .
echo "capataz: built ./bin/capataz" >&2
