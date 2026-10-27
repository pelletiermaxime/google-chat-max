# Next steps

Current features are described in the [README](../README.md#use).
See [testing](testing.md) for local and optional live-test commands.

## Emoji reactions

Quick reactions use the requested fixed trio: 👍, :yep:, and :peepohey:.
A native picker includes nine Unicode category tabs plus Custom, global search,
and a skin-tone selector. The bundled Unicode 18.0 catalog contains 3,963 fully
qualified emoji; All tones exposes mixed-tone variants. Custom GIF emoji and uploaded GIF previews animate while visible
and respect system reduced motion; animations above the decoding limits retain
a still preview.
Counts, own-reaction highlighting, add/remove, desired-state retries, and live
reaction-event reconciliation are implemented. Read-only verification loaded
27 real custom emoji and rendered the favorite images; mutations were verified
with fake services. A real-account add/remove walkthrough remains to be done by
an explicit user action.

## Remaining verification

1. **Physical sleep/wake:** check connection status, incoming messages, open
   threads, and activity badges after the workstation resumes. The overdue-timer
   tests cover software behavior, not an actual suspend/resume cycle.
2. **Overnight use:** observe lease renewals, reconnects, resource cleanup, and
   memory over a longer session. The bounded runtime checks passed; overnight
   stability has not been established.
3. **Long outages:** verify recovery and visible history after disconnection
   exceeds the retained backlog. Document any gaps and the behavior of Latest;
   the bounded backlog/history window cannot guarantee complete recovery.
4. **Live sending walkthrough:** verify composer sends, thread replies, quotes,
   and safe retries against a real account. Automated checks use fake services;
   real Chat test messages require explicit authorization.

## Optional later features

- Persistent drafts across app restarts.
- Message editing/deletion and incoming edit/delete updates.
- Interactive card actions.
- Video and Google Drive previews.
- Synchronization with Google's read state.

System notifications remain excluded; activity indicators stay inside the app.

## Completed maintenance

The four reviewed history/metadata bugs are fixed and covered by permanent
regressions. The paging fixture now respects the 60-message limit. Conversation
and thread history share lifecycle and merge logic, and the obsolete scroll
fields are removed. Repeated renewal/reconnect checks, the native memory/cache
soak, and live cloud renewal/delivery/cleanup checks passed.
