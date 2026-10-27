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
cp README.md "$package/"
cp -R docs "$package/docs"
if [ -f LICENSE ]; then cp LICENSE "$package/"; fi

# Ship the license/notice files from the pinned dependency modules, including
# Unicode's embedded data. These are public dependency files, never host config.
python3 - "$package" <<'PY'
import json
import pathlib
import shutil
import subprocess
import sys

destination = pathlib.Path(sys.argv[1]) / 'THIRD_PARTY_LICENSES'
destination.mkdir()
raw = subprocess.check_output([
    'go', 'list', '-mod=readonly', '-tags=netcgo', '-deps', '-json', './cmd/google-chat-max'
], text=True)
decoder = json.JSONDecoder()
seen = set()
while raw.strip():
    package, end = decoder.raw_decode(raw.lstrip())
    raw = raw.lstrip()[end:]
    module = package.get('Module')
    if not module or module.get('Main') or module['Path'] in seen:
        continue
    seen.add(module['Path'])
    source = pathlib.Path(module['Dir'])
    notices = [p for p in source.rglob('*') if
               p.name.upper().startswith(('LICENSE', 'LICENCE', 'COPYING', 'NOTICE')) and p.is_file()]
    if not notices:
        raise SystemExit(f"No license notice found for {module['Path']}@{module['Version']}")
    target = destination / (module['Path'] + '@' + module['Version'])
    target.mkdir(parents=True)
    for notice in notices:
        output = target / notice.relative_to(source)
        output.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(notice, output)
shutil.copy2('internal/emojidata/LICENSE', destination / 'UNICODE-LICENSE')
goroot = pathlib.Path(subprocess.check_output(['go', 'env', 'GOROOT'], text=True).strip())
shutil.copy2(goroot / 'LICENSE', destination / 'GO-LICENSE')
# Preserve the Unicode license link when reading the bundled guide offline.
unicode_dir = pathlib.Path(sys.argv[1]) / 'internal' / 'emojidata'
unicode_dir.mkdir(parents=True)
shutil.copy2('internal/emojidata/LICENSE', unicode_dir / 'LICENSE')
PY

revision=$(git rev-parse --verify HEAD 2>/dev/null || printf '%s' uncommitted)
if [ -n "$(git status --porcelain)" ]; then revision="$revision (working tree has changes)"; fi
{
  printf 'Google Chat Max %s\nExperimental Linux x86-64 build\n' "$version"
  printf 'Source commit: %s\n' "$revision"
  printf 'Build time (UTC): %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'Build: CGO_ENABLED=1 GOMAXPROCS=6 go build -mod=readonly -tags=netcgo -trimpath -buildvcs=false -p 6 -ldflags="-s -w"\n'
  (cd "$package" && go version -m google-chat-max)
} > "$package/BUILD.txt"
tar -C "$stage" --owner=0 --group=0 --numeric-owner -czf "dist/$name.tar.gz" "$name"
(cd dist && sha256sum "$name.tar.gz" > "$name.tar.gz.sha256")
printf 'Built dist/%s.tar.gz and its SHA-256 checksum.\n' "$name"
