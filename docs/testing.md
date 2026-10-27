# Testing

## Local tests

Run from the repository root:

```sh
sh scripts/check.sh
sh scripts/check.sh --race
GOCACHE="$PWD/.cache/go-build" GOMAXPROCS=2 go vet -p 2 ./...
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
