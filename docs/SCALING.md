# Scaling and runtime limits

Verified against native Go on 2026-10-04. [ADR-0032](adr/0032-complete-native-go-backend.md)
records the runtime boundary; [STATUS](../STATUS.md) records completed qualification and
launch gates. Fixture results are not a production capacity or cost forecast.

## Current bounded deployment

The serving process uses `pgxpool` with minimum zero and maximum four connections by
default. `DB_POOL_MAX_SIZE` accepts two through four; `DB_POOL_MIN_SIZE` cannot exceed
that maximum. `DB_STATEMENT_TIMEOUT_MS` defaults to 5000 and accepts 1 through 30000.
HTTP serving reserves one pool connection for the PostgreSQL live listener. One-shot
migration/job paths do not reserve that listener. Keep enough pool capacity for request,
queue and worker transactions; multiplying replicas multiplies database demand.

Native lists use bounded limits and the existing pagination/cursor contracts. Codec work
has separate byte, pixel, duration, process and concurrency ceilings; it runs outside the
short video claim/finalization transactions. Ordinary request bodies retain an 8 MiB cap.
Only designated multipart media routes admit their explicitly bounded video upload size.
See [FILE_STORAGE](FILE_STORAGE.md) and the [CLI configuration reference](../services/server/cmd/social-server/README.md).

Private bytes and E2EE state are authorized against current account/cohort/consent,
membership, blocking and moderation state. Opaque API tokens and sessions are checked on
use; revocation also closes affected live observers. SQL indexes, batched projections and
keyset pagination help bounded work, but do not establish an unmeasured user capacity.

## Shared-state boundary

General API, social and catalog throttle histories are process-local. Authentication
budgets/state and selected domain budgets use PostgreSQL. Default API admission rates
are anonymous 60/minute, user 240/minute and token obtain 10/minute; configured rates
support positive minute units only. Splitting requests across replicas can multiply a
process-local allowance. PostgreSQL ID-only NOTIFY provides live fan-out, not a shared
cache or a global throttle service.

`REDIS_URL`, required shared-state mode and `SENTRY_DSN` currently reject unsupported
configuration. Adding Redis to a deployment does not implement these native contracts.
A deployment requiring global domain budgets needs a reviewed shared-budget implementation
and qualification before extra serving replicas are enabled.

## Measure before expanding

1. Observe aggregate request/error/duration metrics, database pool wait and query plans,
   queue age/attempts, codec completion and private-storage latency. Do not add per-user
   analytics or log private paths, message bodies, credentials or media.
2. Optimize an evidenced query/serialization/codec bottleneck with bounded fixtures and
   representative synthetic load. Keep cold/warm measurements distinct and report the
   tested workload rather than extrapolating to millions of users.
3. Qualify additional replicas against shared budgets, session/token revocation, observer
   closure, deferred-task claims and privacy gates. Preserve single authoritative writes.
4. If PgBouncer is introduced, provide a session-capable connection for `LISTEN` rather
   than sending the live broker through a transaction-only pool; test the resulting
   connection budget and recovery behavior.
5. Introduce replicas/caches only with explicit freshness contracts. Authorization,
   consent, blocks, revocation and moderation must not read stale permission from a replica
   or TTL cache. Public cache keys must exclude private/cohort-specific representations.

Presigned object redirects are an explicit opt-in throughput trade-off: the application
checks access before redirect, but a copied object URL remains usable until its short
expiry. Streaming is the current default; it checks at request admission, not per byte.
Do not add a public media CDN or relax private-bucket policy as a scaling shortcut.

## Product constraints

Keep Postgres as the primary datastore and preserve the HTTP producer/consumer boundary,
EU private-media requirements, no ads/tracking and no engagement-driven precomputation.
There is no demonstrated need for sharding, a separate graph/vector store or per-user
cloud AI. Provider selection and paid infrastructure remain owner decisions.

See [HOSTING_EU](HOSTING_EU.md), [RUNBOOK](RUNBOOK.md) and
[ASYNC_TASKS](ASYNC_TASKS.md). The earlier scaling roadmap is retained as
[historical reference](archive/scaling-native-go-reference.md).
