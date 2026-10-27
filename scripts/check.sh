#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
export GOMAXPROCS=6
export GOCACHE="$PWD/.cache/go-build"
mkdir -p .cache/testbin
race=""
if [ "${1:-}" = "--race" ]; then race="-race"; fi
# Stable executable paths keep firewall decisions useful across test runs.
# Live Google/cloud tests remain opt-in through their existing environment flags.
for package in settings chat media emojidata desktop; do
  go test -c $race -p 6 -o ".cache/testbin/$package.test" "./internal/$package"
  ".cache/testbin/$package.test" -test.timeout=120s
done
