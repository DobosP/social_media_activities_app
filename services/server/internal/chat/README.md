# Native live transport

Create `NewBroker(db)` and run `broker.Run(serverContext)`. It reserves one pool
connection for PostgreSQL LISTEN/NOTIFY, namespaces channels by `current_schema()`
and accepts identifier-only events of at most 1024 bytes. Domain transactions
call `Publish(ctx, tx, Event)`; rollback broadcasts nothing. Listener loss closes
current sockets so clients reload durable history. A room has at most 256 local
subscribers, a process at most 1024 and each socket a 32-event queue. Transient
typing is dropped for a socket whose queue is at least half full and never closes
it; a slow socket closes independently only when a durable message or attachment
event cannot be queued. Typing publishes are coalesced per (room, actor) for 2
seconds across the process, in a map of at most 4096 entries that evicts expired,
then oldest, entries rather than refusing a new typer.

`NewServer(broker, authority, adapters, DefaultConfig())` registers the existing
`/ws/chat/{room}/` and `/ws/messaging/{room}/` URLs. `authority` must validate the
captured HTTP session or mobile token on every call, including expiry/revocation;
returning a cached actor is insufficient. Each adapter supplies domain read
authorization, inbound processing and a per-viewer payload resolver. Revocation
closes with code 4403. Cross-site origins are rejected; explicit wildcard origin
patterns are unavailable. Compression is disabled. Frames are at most 2 MiB,
writes time out after 10 seconds and idle connections after 30 minutes. Inbound
frames are metered before any authorization or domain work: a 10-frame burst that
refills at 5 frames per second (`FrameBurst`, `FrameInterval`), plus at most one
typing frame per 2 seconds (`TypingInterval`). Excess typing frames are dropped
silently and spend no tokens; any other frame beyond the bucket closes the socket
with 1008.

`messaging.Service.LiveAdapter()` supplies encrypted messaging. For plain threads,
`PlainAdapter` takes the social domain's authorization/write/typing/committed-post
callbacks plus the media domain's per-viewer attachment resolver. The post callback
must preserve the shared safe body markup; attachment callbacks return the existing
`id/kind/url/thumb_url/poster_url/processing/failed/blocked/expired/filename/expires_at`
shape. Typing is transport only, excludes its originating connection and stores no
presence row. Its sender label is resolved after authorization, not placed into
PostgreSQL notification payloads, and once per local fan-out of a notification;
each recipient's own read authorization still runs on every delivery. Video state changes publish an `attachments`
event with the owning thread and post IDs.

No raw content, ciphertext, credentials, client IPs or session identifiers are
logged by this package. The listener carries IDs and resolves durable bytes only
after the receiving viewer's current permissions pass.
