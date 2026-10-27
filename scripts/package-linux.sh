#!/bin/sh
# Build the same Linux x86-64 archive locally and in GitHub Actions.
set -eu
cd "$(dirname "$0")/.."
export GOMAXPROCS=6
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export CGO_ENABLED=1

version="${1:-dev}"
case "$version" in
  ''|*[!a-zA-Z0-9._-]*) echo 'Version must contain only letters, numbers, dots, underscores, or hyphens.' >&2; exit 1 ;;
esac
if [ "$(go env GOOS)/$(go env GOARCH)" != linux/amd64 ]; then
  echo 'Package on a Linux x86-64 host with its native C compiler.' >&2
  exit 1
fi

mkdir -p .cache dist
stage=$(mktemp -d "${TMPDIR:-/tmp}/google-chat-max-package.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
name="google-chat-max-${version}-linux-amd64"
package="$stage/$name"
mkdir -p "$package"
go build -mod=readonly -tags=netcgo -trimpath -buildvcs=false -p 6 \
  -ldflags='-s -w' -o "$package/google-chat-max" ./cmd/google-chat-max
# --help exits before initializing the GUI, credentials, or cloud resources.
"$package/google-chat-max" --help
python3 scripts/package-resources.py "$package" "$package/google-chat-max" "$version" 'Linux x86-64'
tar -C "$stage" --owner=0 --group=0 --numeric-owner -czf "dist/$name.tar.gz" "$name"
(cd dist && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
printf 'Built dist/%s.tar.gz and its SHA-256 checksum.\n' "$name"
