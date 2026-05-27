# PostgreSQL strategy — efficient, secure, performant data access

How this project should use **PostgreSQL 16 + PostGIS** (via Django 5.2 + psycopg 3)
so the database stays fast and safe as it grows. Grounded in the current code; pairs
with [ARCHITECTURE](ARCHITECTURE.md), [SECURITY](SECURITY.md), and [RUNBOOK](RUNBOOK.md).

Status legend: ✅ in place · ▶️ recommended next · ⏳ later/scale.

## 1. Connections — reuse them (biggest quick win)

The app currently opens a **new connection per request** (`env.db()` with no
persistence). Opening a Postgres connection is expensive; reuse is the highest-impact
change.

- ▶️ **Persistent connections.** Set `CONN_MAX_AGE` (e.g. 60s) and
  `CONN_HEALTH_CHECKS=True` so Django reuses a live connection across requests and
  drops dead ones:

  ```python
  # config/settings/base.py — after DATABASES = {...}
  DATABASES["default"]["CONN_MAX_AGE"] = env.int("DB_CONN_MAX_AGE", default=60)
  DATABASES["default"]["CONN_HEALTH_CHECKS"] = True
  ```

- ▶️ **Server-side pooling for multi-worker deploys.** With gunicorn/daphne running
  several workers, `CONN_MAX_AGE` keeps `workers × threads` connections open. psycopg 3
  supports a built-in pool (Django 5.1+):

  ```python
  DATABASES["default"]["OPTIONS"] = {"pool": {"min_size": 2, "max_size": 10}}
  ```

  ⏳ At higher scale put **PgBouncer** (transaction pooling) in front instead, and set
  `CONN_MAX_AGE=0` so the app leans on the bouncer. Transaction pooling forbids
  session-level state (no server-side cursors / `SET`), which suits this app.
- Keep `max_connections` on the server modest; size the pool to it. A managed EU
  Postgres (per RUNBOOK) typically caps connections low on small plans.

## 2. Indexing — match indexes to the actual access paths

The schema already declares ~47 indexes/constraints. Principles to keep:

- ✅ **Composite indexes ordered by selectivity / query shape.** e.g.
  `Activity(cohort, status)`, `Membership(activity, state)`,
  `Post(thread, created_at)` — these mirror the real filters (cohort-scoped lists,
  members of an activity, a thread's posts in order). Leftmost-prefix rule: one
  `(a, b)` index also serves filters on `a` alone.
- ✅ **Unique constraints as integrity *and* index** — `uq_membership_activity_user`,
  `uq_joinvote_membership_voter` prevent dupes and speed lookups.
- ✅ **Spatial (GiST) index on geography.** `Place.location =
  PointField(geography=True, srid=4326)` — GeoDjango creates the GiST index
  automatically; keep it (don't set `spatial_index=False`). It powers
  `ST_DWithin`/distance ordering used by the places proximity API.
- ▶️ **Partial indexes** for skewed status columns queried by one value, e.g. open
  activities or pending join requests:

  ```python
  models.Index(fields=["starts_at"], name="ix_activity_open",
               condition=Q(status="open"))
  ```

- ▶️ **Covering/`include` indexes** (Postgres 11+) when a hot query reads a couple of
  extra columns, to get index-only scans.
- ▶️ **Trigram (`pg_trgm`) GIN index** for name search (places/activities) instead of
  `LIKE '%x%'` scans; add the extension via a migration `CreateExtension("pg_trgm")`.
- ⚠️ Don't over-index: every index slows writes and ingestion (`ingest_places` does
  bulk upserts). Add an index only for a query you actually run; verify with `EXPLAIN`.

## 3. Querying — the ORM patterns that avoid N+1 and over-fetch

- ✅ **`select_related`** (FK joins) and **`prefetch_related`** (reverse/M2M) are
  already used in the hot list views (places, social, taxonomy, events, booking).
  Keep every list endpoint that renders related data covered.
- ▶️ **`only()` / `defer()`** on wide rows you don't fully render (e.g. `Place.raw_tags`
  JSON, large text) to cut row width and memory.
- ▶️ **Keyset (cursor) pagination** for large, append-only lists (posts, chat history,
  events) instead of `LIMIT/OFFSET` — offset scans degrade on deep pages. DRF's
  `CursorPagination` on `created_at`/`id`.
- ✅ **Aggregate in the DB**, not Python — `Count`, `Sum` (e.g. donation totals, member
  counts). Avoid `len(queryset)` when you only need `.count()`.
- ✅ **Wrap multi-write operations in `transaction.atomic`** (join-by-vote, donations,
  ingestion upserts already do). Use `select_for_update()` for read-modify-write races
  (e.g. completing a donation, admitting a member) — already applied in donations.
- ▶️ **Bulk operations** for ingestion: `bulk_create`/`bulk_update` with
  `update_conflicts`/`ignore_conflicts` rather than per-row saves.
- ▶️ Add **`django-debug-toolbar`** (dev only) or assert query counts in tests
  (`assertNumQueries`) on key endpoints to catch N+1 regressions in CI.

## 4. PostGIS specifics

- Use **geography** (not geometry) for lon/lat so distances are in metres without
  reprojection — already the case.
- Filter then measure: `ST_DWithin` (uses the GiST index) before ordering by
  `Distance`, so proximity queries stay index-backed.
- The PostGIS extension is created automatically by the GeoDjango backend's
  `prepare_database()` during `migrate`; the DB role needs `CREATE EXTENSION` rights on
  first deploy (see RUNBOOK).

## 5. pgvector readiness (Phase 2 recommendations)

`PHASE_2_PLAN` P3 plans interest-similarity recommendations with **pgvector**:
- Add `pgvector` (Postgres extension) via `CreateExtension("vector")`, store embeddings
  in a `VectorField`, and index with **HNSW** (`vector_cosine_ops`) for fast ANN.
- Keep embeddings in their own table/columns so the core write path isn't slowed.

## 6. Security

- ✅ **Parameterized everything** — go through the ORM; never f-string user input into
  SQL. If raw SQL is ever needed, use `params=[...]` (never `%` formatting).
- ▶️ **Least-privilege roles.** App connects as a role that owns its tables but is *not*
  a superuser. Use a separate migration/admin role for `CREATE EXTENSION`/DDL, and a
  read-only role for analytics/`/api/ops/stats`.
- ▶️ **TLS to the database** in production: `OPTIONS={"sslmode": "require"}` (or
  `verify-full` with a CA) — managed EU Postgres supports it.
- ✅ Secrets only via env (`DATABASE_URL`), never committed.
- ▶️ Statement timeout to bound abuse / runaway queries:
  `OPTIONS={"options": "-c statement_timeout=5000"}` (5s).
- ✅ Data minimization (age band not DOB, no card data, EXIF stripped) keeps the most
  sensitive data out of the DB entirely — see COMPLIANCE/SAFETY.

## 7. Observability & tuning

- ▶️ Enable **`pg_stat_statements`** to find the costliest/most frequent queries.
- ▶️ Log slow queries (`log_min_duration_statement`) and review `EXPLAIN (ANALYZE,
  BUFFERS)` for them before adding indexes.
- ✅ Health/readiness probes hit the DB (`/healthz`) so a DB outage sheds traffic.
- ⏳ Routine `VACUUM`/`ANALYZE` is autovacuum's job; watch dead-tuple bloat on
  high-churn tables (chat messages, audit log) and tune autovacuum per-table if needed.
- Backups + tested restores: see RUNBOOK.

## Checklist (do in this order)

1. ▶️ `CONN_MAX_AGE` + `CONN_HEALTH_CHECKS` (cheapest, biggest win).
2. ▶️ TLS (`sslmode`) + `statement_timeout` in prod `OPTIONS`.
3. ▶️ Least-privilege app role; separate DDL/read-only roles.
4. ▶️ `assertNumQueries` guards on hot endpoints; fix any N+1.
5. ▶️ Partial/trigram indexes where `EXPLAIN` shows scans; keyset pagination on feeds.
6. ⏳ psycopg pool / PgBouncer and pgvector when scale / Phase 2 require them.
