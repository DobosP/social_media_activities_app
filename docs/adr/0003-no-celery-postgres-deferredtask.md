# ADR-0003: No Celery/Redis broker — Postgres `DeferredTask` queue drained by the existing cron

Date: 2026-06-23
Status: accepted

## Decision
Do off-request work with a **Postgres-backed** durable queue: `apps/ops/models.py:DeferredTask` +
`apps/ops/tasks.py` (`register`/`enqueue`), drained by `process_deferred_tasks` as the last job in
the existing `run_due_jobs` cron. Transactional enqueue, at-least-once idempotent handlers,
`SELECT ... FOR UPDATE SKIP LOCKED` claims, bounded exponential-backoff retries, `dedup_key`
at-most-one-PENDING. No Celery/RQ/Dramatiq, no Redis-as-broker, no new process. Shipped `60e1845`
(2026-06-23); the operating contract is `docs/ASYNC_TASKS.md`.

## Context / why
Some work is too heavy for the request thread (GDPR-erasure blob cleanup, media scan execution,
large notification fan-out), but the product invariants pin the solution: **Postgres is the single
datastore** and we avoid heavy deps and per-user cloud spend (CLAUDE.md inv. 6).
- **Why not Celery/RQ (+ Redis broker)**: a second stateful infrastructure component + worker
  process for a donations-funded single-box launch; Redis here is an optional cache/channel layer,
  never a source of truth.
- **Why not django-tasks/procrastinate/django-q2**: `PRODUCTION_READINESS.md` §2b listed them as
  candidates; a ~200-line in-repo foundation on the already-running cron gives the same seam with
  zero new deps, and the `enqueue`/handler API can be pointed at a dedicated worker loop or a real
  broker later without touching call sites.
- **Safety rule (load-bearing)**: deferral only moves *already-authorised* work. Fail-closed gates
  (media scan admission, cohort/consent/block checks) stay on the request path — admit-then-scan
  is a child-safety regression (`ASYNC_TASKS.md` §safety).

## Consequences
- Latency = cron-tick granularity; fine for cleanup/fan-out. A near-real-time kind later needs a
  dedicated drainer loop (same API).
- Every handler MUST be idempotent (at-least-once), payloads are IDs only (rows are plaintext in
  Postgres — no PII/secrets).
- As of 2026-07-02 **no production task kind is registered yet** — first callers per the
  `ASYNC_TASKS.md` migration plan (erasure blob cleanup recommended first).
- Supersedes: `PRODUCTION_READINESS.md` §2b's "no task queue exists — add Celery/django-tasks"
  framing (2026-06-19). Superseded-by: none.
