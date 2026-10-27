# Architecture

## Code map

| Area | Responsibility |
| --- | --- |
| `cmd/google-chat-max/` | Flags, native window, single-instance lock, shutdown |
| `internal/settings/`, `internal/desktop/launcher.go` | Non-secret project configuration, first-run setup, auth-tool checks and safe project switching |
| `internal/chat/auth.go` | Existing host `gws` credentials and OAuth token source |
| `internal/chat/auth_status.go` | Bounded local `gws auth status` checks; only setup metadata reaches the UI |
| `internal/chat/client.go`, `history.go` | Conversation metadata and paginated history |
| `internal/chat/identity.go`, `send.go` | Membership identities and explicit message sends |
| `internal/chat/reactions.go` | Custom emoji catalog, verified own reactions, explicit desired-state changes |
| `internal/emojidata/`, `scripts/update-emojis.py` | Bundled, licensed Unicode 18.0 catalog and reproducible data generator |
| `internal/chat/stream*.go` | StreamingPull, lease renewal, resource journal and cleanup |
| `internal/desktop/` | Main-thread state, native views, drafts, paging, activity and avatars |
| `scripts/check.sh`, `relaunch.sh` | Stable test executables and graceful Linux relaunch |

The UI uses MyGo v0.6.4. The base backend exposes conversation/history reads;
optional interfaces add threads, page cursors, sends, and live events. Demo mode
uses synthetic fixtures, including synthetic sends; refreshing restores fixture
history rather than emulating a persistent server.

## State and request flow

The launcher reads project configuration from the platform user-config directory
before opening the Chat backend. Missing/invalid configuration displays native
setup and checks for `gws`, saved OAuth metadata, and optional `gcloud` support
for first-time client setup. These checks run in workers. Configured launches
open Chat without waiting for the setup-only status command. Demo bypasses both.
Saving stores only the project ID, atomically, with private file permissions.
Project switches wait for the old App's ordinary shutdown, including pending
mutations and cloud cleanup; window closure during that wait cannot start a new
App. Each backend retains an immutable project selection.

Authentication and network calls run in workers. Production dispatch uses
`mygo.RunOnMain`, which waits for the UI callback to finish. State changes and
bitmap creation happen there. Conversation/thread request generations reject
late responses after selection changes. Separate page cursors and native list
states belong to the conversation and open thread.

Conversation listing performs only list-page requests and reuses cached identity
data. The sidebar is published before resolving the account profile or any
uncached memberships. A separate background batch resolves the profile, then
enriches up to 32 human DM/group conversations in descending activity order with
four workers. Results update names, avatars, and participants on the main thread
without replacing live timestamps, selection, or activity badges. Each lookup
has a 10-second timeout within a 45-second batch budget. Refresh cancels the old
batch and rejects its queued results by generation; close cancels all work.
Existing identity fields remain visible while a new list refreshes.

Conversation and thread history starts with 20 messages; older pages request up
to 60. History uses already-cached sender identities without making membership
requests. Selecting or refreshing a conversation resolves its members in a
separate worker with a 10-second timeout, so messages can appear first. Switching
conversations cancels that lookup and rejects late identity results.

Leaving a successfully loaded conversation transfers its messages, page cursor,
retention flags, and native list state into an in-memory LRU cache of 12 inactive
views. Returning transfers that state back immediately. The latest successful
history-fetch timestamp controls a 30-second freshness window; older entries
remain visible during background revalidation. Empty successful results are
cached, while incomplete/failed first loads are not. Incoming events update
inactive cached windows without clearing activity badges or extending freshness.
Recent-window overflow invalidates the cursor and freshness so dropped boundary
messages cannot be skipped by paging; expanded windows expose the Latest action
when newer history is discarded. Reconnect invalidates all freshness timestamps,
and the Refresh messages action always starts a request. No message cache is
written to disk.

A shared history lifecycle binds conversation/thread requests, overlays, page
cursors, retention flags, and native list state. Paging protects the boundary
before starting network work and rechecks the current anchor before applying a
page. A response that would evict the visible message is postponed without
advancing its cursor. One metadata-preserving merge handles both views and their
inactive caches; out-of-order overflow exposes the newer-history indicator.
Failed snapshots retain their live overlay for retry, and reconnect during an
active thread fetch queues another snapshot. Native `ListState` alone owns
scrolling. Recovery remains limited by the bounded history/backlog.

Drafts are keyed by conversation and thread. A send captures text, destination,
and a UUID request ID. Uncertain delivery keeps the draft and offers an explicit
retry with the same ID. Named thread replies request `REPLY_MESSAGE_OR_FAIL`.
Quoted drafts also capture the original message's name and last-edit timestamp
(or creation timestamp for an unedited message). The send uses Google's
`quotedMessageMetadata`; its output-only snapshot stays out of the request body.
Retries preserve the quote target/version, text and request ID together. The UI
locks quote replacement/removal while sending or delivery remains uncertain.
Quoting a thread reply targets that thread; quoting a root from the main
conversation creates a new root quote reply. Source previews are bounded to 600
runes and three visible lines. A stable composer key preserves keyboard focus
as thread loading indicators and quote previews appear or disappear.
The response and incoming event are deduplicated by message name. The UI waits
for pending sends to settle before canceling the live session on ordinary close.

Reaction changes use a fixed desired state, so retrying an uncertain add never
removes an already-applied reaction. Before writing, the client lists reactions
filtered by the resolved authenticated user ID and verifies returned ownership
and message resource names. Deletion can target only those verified reactions.
Reaction reads and custom-image downloads each use two background workers.
UI changes and bitmap creation return to the main thread. Closing also waits for
pending reaction mutations, using the existing pending-action lifecycle.

## Live-resource lifecycle

1. Configured startup connects automatically after the conversation backend is
   ready, retries cleanup of any journaled prior session, then records a new
   random ownership ID before creating cloud resources.
2. It creates a labeled private topic and receiving subscription in the selected project.
   Publisher IAM is set only on that topic.
3. A user-authenticated Workspace subscription targets
   `//chat.googleapis.com/spaces/-` for message-created and reaction-created/deleted
   events with full resource data, decoded into bounded typed models.
4. Pub/Sub receives over gRPC StreamingPull. A valid message is acknowledged
   after UI application; unknown types are ignored and malformed payloads request
   selected-history reconciliation. Reaction events invalidate and refresh the
   affected visible reaction state, coalescing events during a request. They do
   not increment message activity. Message edits/deletions remain unsubscribed.
5. A 15-minute Workspace lease is renewed five minutes before expiry. Terminal
   receive errors use bounded backoff; the SDK also retries transient failures.
   Renewal failure or a suspension/expiration stops this session and requires
   retrying through the error-only Retry incoming messages action or Settings.
   Temporary transport gaps reconnect automatically; healthy connections have no
   status label or connection controls in the UI.
6. Window closure or a project switch cancels receiving, joins renewal work, and attempts cleanup
   under an independent 90-second context: Workspace subscription, receiving
   subscription, then topic. Server labels and exact topic endpoints establish
   ownership before deletion. Cleanup errors retain the journal for a later retry.

The journal is `$XDG_STATE_HOME/google-chat-max/live-resources.json`, defaulting to
`~/.local/state/google-chat-max/live-resources.json`. It stores ownership and
operation metadata and the originating project, never tokens or message bodies.
Cleanup uses that recorded project even after settings change. Journals without
a project stop recovery before any cloud request; the original project must be
restored explicitly, as described in the README. New sessions require explicit
project configuration. The topic has no automatic
expiry; after a crash, reopening the configured app attempts recovery automatically.
The receiving subscription expires after one day of inactivity. Backlog retention
is ten minutes. Retained unacknowledged events and a selected-history refresh help
on reconnect, but do not guarantee complete recovery after long gaps.

## Bounds and privacy

| State/work | Current bound |
| --- | --- |
| Conversation listing | 10 pages / 1000 conversations |
| Recent history / retained view | 20 initially, 60 per older page / 300 history messages per view, plus a thread parent |
| Conversation cache | 12 inactive views, each with the same history bound; 30-second freshness |
| REST response / decoded event | 4 MiB / 1 MiB |
| StreamingPull outstanding work | 8 messages / 1 MiB |
| Message dedup / conversation activity / thread activity | 2048 / 1000 / 256 entries |
| UI identity cache | 1024 users |
| Membership cache | 64 spaces, 10-minute freshness, at most 200 members per lookup |
| Sidebar identity enrichment | Background batch: 32 most recent human DM/group chats, 4 workers, 45 seconds |
| Drafts | 128 destinations; composer clips text to 28,000 UTF-8 bytes |
| Avatars | 2 workers, queue of 32, 64 cache entries |
| Image input / decoded source / thumbnail | 1 MiB / 1024×1024 / 64×64 |
| Native text cards | 4 cards per message; 10 sections, 20 widgets per section, 32 text blocks and 4000 displayed runes per card |
| Inline image work | 2 workers, 16 queued jobs; 20-second job timeout; conversation switches cancel pending work |
| Inline image input / preview | 10 MiB download, 16 million pixels, 16384 pixels per side; resized to at most 960×720 |
| Inline image cache | 32 entries and 24 MiB of base bitmap pixels plus retained animation frames; LRU eviction; no original files retained |
| GIF decoding | At most 256 frames, 4 million logical canvas pixels, 32 million total source frame pixels; descriptors checked before full decoding |
| Inline GIF previews | At most 480×360 per frame, downscaled further to fit 8 MiB per animation within the image cache |
| Custom emoji catalog | 1000 emoji, refreshed on use after 8 minutes; no frequency-history scan |
| Standard emoji catalog | 3,963 fully qualified Unicode 18.0 entries, bundled offline; nine category tabs plus Custom; virtual grid builds visible rows only |
| Emoji sprites | 2 workers, 32 queued jobs, 64 cached images; at most 64×64 per frame and 512 KiB retained GIF frames per sprite (about 33 MiB total with base bitmaps); same 1 MiB/1024×1024 unauthenticated Google-host limits as avatars |
| Reaction state | 64 message entries; 2 read workers, 16 queued reads, 15-second read timeout; 40 visible summary chips per message |
| Reaction mutations | At most 8 total pending actions when starting a reaction; 30-second timeout; retry requires a click |

For display, message text above 16,000 bytes is shortened to at most 4,000 runes
with a visible notice. The retained message object is not shortened by that view.
Card text uses a bounded HTML tokenizer to extract text and line breaks into
native selectable labels; it does not embed a document or execute card actions.
Both legacy cards and cardsV2 are requested in history and live resource payloads.
Message sender names/avatars take precedence over incomplete membership records.

Avatar requests use a plain HTTP client with no OAuth transport, restricted to
Google HTTPS domains and checked redirects. Unavailable identities/images fall
back to IDs or initials. The avatar cache currently has no eviction/retry policy
once an entry occupies a slot. Identity enrichment is best effort and incomplete
for conversations or memberships beyond its bounds.

Uploaded image previews use the user-authenticated Chat `media.download` endpoint
with `attachmentDataRef.resourceName`. The client escapes that opaque reference
as a path component and rejects redirects, keeping its OAuth transport on the
trusted API endpoint. Browser download/thumbnail URLs are not used. Downloads,
decoding and downscaling happen in workers; only bitmap creation and view state
updates run on the main thread. PNG, JPEG, WebP and GIF are supported. GIF frames
are composited with transparency and disposal in workers. Visible images use
MyGo paint timers at each frame delay and update one bitmap without rebuilding
the view; offscreen images schedule no further playback. Finite loop counts are
honored. System reduced motion freezes the first frame. Animations exceeding the
decoding limits fall back to a still preview. Other attachments remain labeled.
Preview failures wait for an
explicit retry. Cached image data stays in memory, with no media files on disk.

Authentication exports credentials from `gws` into process memory only. The token
source can still refresh during cleanup after window cancellation, with bounded
HTTP timeouts. Error messages omit response bodies and credentials. No background
service, autostart, Google read-state writes, or system notifications are added.
