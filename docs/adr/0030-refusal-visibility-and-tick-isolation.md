# ADR-0030: A refused RO-EDU product is loud, and an opt-in source never red-lines the compliance tick

- Status: accepted (proposed 2026-08-18; supersedes nothing)
- Date: 2026-08-18
- Owner decision pending on one point: whether the deliberate narrowing in §4 (a refused
  `venues` product fails the whole legacy run) is the posture you want.

## Context

Run against a live `ro_data_server` on 2026-08-18, every RO-EDU products page answered
`{"available": false, "note": "schema not ready: … missing required policy column(s) …"}`.
`ingest_places --source=roedu` printed `places: created=0` and `sync_roedu_events` printed
`applied 0 events`, both exit 0 — byte-identical to what a city with genuinely no data
produces. The server's `note`, which said exactly why, was discarded by the walk.

That is not a cosmetic gap. The whole session that found it was spent establishing, from the
producer side, a fact the consumer already knew and had thrown away.

The shared vendored client (`_roedu_client_core.py`, ADR-0069 in `romania_scraper`) ends its
walk on `available: false` deliberately and silently, and it is SHA256-stamped: it is the
producer's file, and not the place to encode this app's operational preference.

## Decision

1. **A refusal is an error in this app's layer, not an empty result.**
   `RoeduClient.iter_required` raises `RoeduProductUnavailable` carrying the server's note.
   It applies to the mid-walk case too, which was worse than the empty first page: a refusal
   on page three truncated the result set with no signal, handing the caller a
   plausible-looking count. The note is collapsed to one line and bounded at 500 characters
   because it is producer-controlled text that lands in cron logs and Sentry.

2. **Both delivery lanes refuse.** `read_app_pack` raises when a pack comes back with zero
   items *because the producer withheld them or reported errors*. The app-pack lane is what a
   promoted release uses, so leaving it silent would have left the defect where it matters
   most. Items dropped by this app's OWN canonical checks stay non-fatal: they make the read
   incomplete so absence is never reconciled, which is an existing tested contract
   (`test_duplicate_or_dangling_items_make_read_incomplete`) and a separate decision.

3. **The scheduled job isolates the refusal rather than failing the tick.** `sync_roedu`
   catches it, logs with a stack, reports to Sentry when configured, writes to stderr, and
   still runs `resolve_place_covers` before completing. Two reasons, both load-bearing:
   `run_due_jobs` pings its heartbeat only when every job succeeded, and that tick carries
   `purge_messaging`, `lift_suspensions`, `consent_renewal_sweep` and the rest of the GDPR/DSA
   duties — an opt-in external source must not withhold the compliance heartbeat nightly for a
   server-side condition outside this app's control, nor bury a real safety-job failure in the
   noise. And cover resolution is city-scoped, so an RO-EDU outage must not freeze Commons
   covers for OSM-sourced places. This matches `sync_event_feeds`, which isolates one bad feed
   by design. **Only a refusal is isolated**: a missing seed, a stale-snapshot guard, a wrong
   pack identity or any other contract breach is this app's own failure and still fails the
   job, because swallowing those would recreate the silent zero one level up.

4. **Deliberate narrowing.** In legacy mode a refused `venues` product now fails the run
   instead of syncing events against whatever places already exist. A venues refusal means the
   venue lane is broken, and event→place linkage would degrade silently — the failure mode this
   ADR exists to remove. The cost: a key scoped to `events` but not `venues` can no longer sync
   events at all.

## Consequences

- Run either command by hand and a refusal exits non-zero with the server's reason.
- A nightly tick against a refusing server completes, pings the heartbeat, and leaves an ERROR
  line plus a Sentry event. Detection depends on Sentry/log monitoring being configured;
  `OPS_HEARTBEAT_URL` and `SENTRY_DSN` are both empty by default.
- Plain `iter` keeps the core's semantics for callers that genuinely want best-effort records.
- A genuinely empty product or pack stays quiet: turning "no venues this week" into a failed
  sync would be the mirror-image bug.
