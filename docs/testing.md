# Development

## Build

Install Go 1.27.3+, a C compiler (`build-essential` on Ubuntu or `base-devel` on
Arch), and the [Linux runtime packages](setup.md#linux-packages).

```sh
GOMAXPROCS=6 go build -mod=readonly -tags=netcgo -p 6 -o bin/google-chat-max ./cmd/google-chat-max
./bin/google-chat-max -demo
```

Check the effective cgroup memory limit before substantial builds or tests.
Use `GOMAXPROCS=6` and `-p 6`; stop if a process is killed for running out of memory.

The Linux build uses the system DNS resolver unless `GODEBUG=netdns=...` overrides
it. Python 3 is needed for packaging, emoji regeneration, and the relaunch helper.
`sh scripts/relaunch.sh` closes this checkout's running app, waits for cleanup,
and opens the rebuilt binary.

## Local tests

Run from the repository root:

```sh
sh scripts/check.sh
sh scripts/check.sh --race
GOCACHE="$PWD/.cache/go-build" GOMAXPROCS=6 go vet -p 6 ./...
```

The check script builds test executables in `.cache/testbin/` and runs tests
against local fake services and native UI fixtures. Leave `CHAT_MAX_LIVE_TEST`
and `CHAT_MAX_LIVE_STREAM_TEST` unset for local testing; the script preserves
explicitly exported opt-ins. Some tests open loopback HTTP servers.

After building the test executables, the optional native history/cache soak test
can run without Google access:

```sh
CHAT_MAX_SOAK_TEST=1 ./.cache/testbin/desktop.test \
  -test.run '^TestBoundedHistoryRuntimeSoak$' -test.v -test.timeout=60s
```

## Read-only account tests

These require the host's saved `gws` login and the app's configured project.
Set `CHAT_MAX_TEST_PROJECT` to use a different project. Output contains counts
and metadata rather than message bodies.

```sh
GODEBUG=netdns=cgo CHAT_MAX_LIVE_TEST=1 ./.cache/testbin/chat.test \
  -test.run '^TestLiveReadOnly$' -test.v -test.timeout=120s

GODEBUG=netdns=cgo CHAT_MAX_LIVE_TEST=1 ./.cache/testbin/chat.test \
  -test.run '^TestLiveHistoryPages$' -test.v -test.timeout=120s
```

## Cloud transport test

Run only when intentionally testing live cloud resources. It creates an isolated
Workspace Events subscription and private Pub/Sub resources, checks renewal and
synthetic event delivery, then checks cleanup. Cloud usage may incur charges.

```sh
GODEBUG=netdns=cgo CHAT_MAX_LIVE_STREAM_TEST=1 ./.cache/testbin/chat.test \
  -test.run '^TestLiveStreamTransportRenewalAndCleanup$' -test.v -test.timeout=4m
```

The synthetic event goes only to the test's private Pub/Sub topic. These tests
send no Chat messages or reactions. Testing real posting or reactions requires
an explicit user action or authorization.

## Release builds

Pushing a new `v*` tag runs the [release workflow](../.github/workflows/release.yml).
It tests, vets, and packages Linux x86-64 on Ubuntu 22.04, Apple Silicon on
`macos-15`, and Intel Macs on `macos-15-intel`. Publication waits for all three
platforms and creates a regular GitHub release marked as latest. A manual
workflow run builds and checks the packages without publishing a release.

Linux gets a `.tar.gz`; each Mac architecture gets a `.dmg` with a native
`.app` and a drag-to-Applications shortcut. Packages include documentation,
third-party notices, and `BUILD.txt`; every download has a separate SHA-256 file.
The workflow uses GitHub's built-in token and needs no Google credentials.

To build the same package locally:

```sh
sh scripts/package-linux.sh dev
```

On a Mac with Xcode command line tools, build a package for that Mac's architecture:

```sh
sh scripts/package-macos.sh dev
```

Mac packages target macOS 13+ and use ad-hoc signing. The packaging step validates
the app's plist and signature and verifies the disk image. It does not use a
Developer ID or notarization credentials; first launch needs macOS approval.

Packages go in `dist/`. Local Linux builds use the host's libc; the glibc 2.35
baseline applies to the Ubuntu-built release assets. Published releases aren't overwritten.
If publication fails, check for an incomplete draft before rerunning the workflow.
The application code still needs a license before a public open-source release.

## README screenshot

After building the test executables, capture the native demo view with:

```sh
CHAT_MAX_SCREENSHOT="$PWD/docs/images/google-chat-max.png" ./.cache/testbin/desktop.test \
  -test.run '^TestNativeConversationSelectionSearchRefreshAndEmptyHistory$' -test.timeout=30s
```

This uses synthetic conversations and makes no Google calls.
