#!/bin/sh
# Build a native macOS app bundle and drag-to-Applications disk image.
set -eu
cd "$(dirname "$0")/.."
export GOMAXPROCS=6
export GOCACHE="${GOCACHE:-$PWD/.cache/go-build}"
export CGO_ENABLED=1
export MACOSX_DEPLOYMENT_TARGET=13.0

version="${1:-dev}"
case "$version" in
  ''|*[!a-zA-Z0-9._-]*) echo 'Version must contain only letters, numbers, dots, underscores, or hyphens.' >&2; exit 1 ;;
esac
if [ "$(uname -s)" != Darwin ] || [ "$(go env GOOS)" != darwin ]; then
  echo 'Package on macOS with Xcode command line tools installed.' >&2
  exit 1
fi
arch=$(go env GOARCH)
case "$arch" in
  arm64|amd64) ;;
  *) echo 'macOS packages support arm64 and amd64.' >&2; exit 1 ;;
esac

mkdir -p .cache dist
stage=$(mktemp -d "${TMPDIR:-/tmp}/google-chat-max-package.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
name="google-chat-max-${version}-macos-${arch}"
app="$stage/Google Chat Max.app"
contents="$app/Contents"
binary="$contents/MacOS/google-chat-max"
mkdir -p "$contents/MacOS" "$contents/Resources"
go build -mod=readonly -tags=netcgo -trimpath -buildvcs=false -p 6 \
  -ldflags='-s -w' -o "$binary" ./cmd/google-chat-max
"$binary" --help
python3 scripts/package-resources.py "$contents/Resources" "$binary" "$version" "macOS $arch"
python3 - "$contents/Info.plist" "$version" <<'PY'
from pathlib import Path
import plistlib
import re
import sys

version = sys.argv[2].removeprefix('v')
if version == 'dev':
    version = '0.0.0'
if not re.fullmatch(r'[0-9]+(?:\.[0-9]+){0,2}', version):
    raise SystemExit('macOS app versions must have one to three numeric components, optionally prefixed with v.')
plist = {
    'CFBundleName': 'Google Chat Max',
    'CFBundleDisplayName': 'Google Chat Max',
    'CFBundleIdentifier': 'com.pelletiermaxime.google-chat-max',
    'CFBundleVersion': version,
    'CFBundleShortVersionString': version,
    'CFBundleExecutable': 'google-chat-max',
    'CFBundlePackageType': 'APPL',
    'CFBundleInfoDictionaryVersion': '6.0',
    'LSMinimumSystemVersion': '13.0',
    'NSPrincipalClass': 'NSApplication',
    'NSHighResolutionCapable': True,
    'NSSupportsAutomaticGraphicsSwitching': True,
}
Path(sys.argv[1]).write_bytes(plistlib.dumps(plist))
PY
printf 'APPL????' > "$contents/PkgInfo"
plutil -lint "$contents/Info.plist"
# Ad-hoc signing needs no certificate or credentials; it is not notarization.
codesign --force --sign - --timestamp=none "$app"
codesign --verify --strict --verbose=2 "$app"
ln -s /Applications "$stage/Applications"
hdiutil create -volname 'Google Chat Max' -srcfolder "$stage" -ov -format UDZO "dist/$name.dmg"
hdiutil verify "dist/$name.dmg"
(cd dist && shasum -a 256 "$name.dmg" > "$name.dmg.sha256")
printf 'Built dist/%s.dmg and its SHA-256 checksum.\n' "$name"
