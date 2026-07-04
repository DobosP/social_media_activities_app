# Scaling & mobile-readiness roadmap

> W10 (2026-06). Where the system stands for "millions of users, cheap to run", what
> shipped now, and the ordered backlog for when load actually arrives. Postgres stays
> the single primary datastore (inv. 6); everything below scales that posture, it never
> replaces it.

## Already solid (verified in the W10 audit)

- **Bounded queries everywhere**: discovery feeds hard-cap at 100, places at 500,
  notifications/conversations at 100; threads use keyset pagination; web lists paginate.
  No endpoint materializes an unbounded queryset.
- **N+1 discipline**: `with_counts` annotation on activity lists, `attach_interest_nodes`
  batches avatars (one query), thread replies prefetched with a filtered queryset,
  `recent_report_n` annotated on places.
- **Indexes on hot paths**: Activity (cohort,status)/(starts_at)/(cohort,type)/(cohort,
  place) + W1 trigram GIN for search; Post (thread,reply_to,created_at);
  GroupMembership (group,state)/(user,state); Notification (recipient,read_at);
  Event (place,starts_at)/(starts_at) + trigram.
- **Connection hygiene**: prod sets `CONN_MAX_AGE=60`, health checks, a 30s statement
  timeout (DoS guard), request-body caps, global DRF throttles (shared via Redis).
- **EU residency**: S3 region pinned EU; Redis for cross-process cache/Channels.

## Shipped in W10

- **Mobile auth**: opaque DRF tokens (`rest_framework.authtoken`) alongside session auth.
  `POST /api/auth/token/` (hard-throttled, `DELETE` revokes). No JWT by design: opaque
  tokens are server-validated, instantly revocable, and carry no PII.
- **Settings API**: `GET/PUT /api/accounts/me/settings/` (notification mutes + access
  needs) — the last web-only preference surface a native client needed.
- **Feed API**: `GET /api/discovery/feed/` (W2) — the typed home feed, identical
  composition to the web home.
- **M2M ingestion API** (W9) for the external aggregator (admin/token-gated).
- **Media off the app process** (2026-07-04): opt-in presigned object-store redirects
  after the viewer authorization check, with local filesystem streaming fallback.

## Ordered backlog (do when load justifies, not before)

1. **Read-replica routing**: a database router sending the read-only hot paths
   (visible_activities lists, feeds, thread pages) to a replica. All gates are
   query-level, so replica reads stay safe.
2. **pgbouncer (transaction pooling)** in front of Postgres once connection counts climb.
3. **Conditional GETs / fragment caching**: ETags on activity/event lists keyed on
   max(updated_at); 5–10 min cache for taxonomy + public places responses.
4. **blocked_user_ids caching** (60s TTL, invalidated on block/unblock) if profiling
   shows it hot — it runs on every visibility check.
5. **Async media scanning** only if external scanner latency (Arachnid/PhotoDNA round
   trip) starts failing uploads — keep fail-closed semantics if so.
6. **Keyset cursors on the remaining offset-paginated lists** for mobile clients.

## Non-goals (product invariants, not debt)

No sharding/multi-primary, no separate vector/graph DB, no per-user cloud-AI spend, no
engagement-driven precomputation ("trending" caches etc. don't exist on purpose).
