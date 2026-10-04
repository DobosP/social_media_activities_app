# PostgreSQL strategy — native Go data access

Runtime selection: [ADR-0032](adr/0032-complete-native-go-backend.md). PostgreSQL16 with
PostGIS and pgvector remains the primary store; `services/server` accesses it through pgx.
Django models/migrations remain an offline compatibility oracle, not a serving dependency.

## Connections and query bounds

The native CLI constructs one bounded pgx pool: default maximum4, configurable2–4,
minimum0, explicit PostgreSQL URL and credential fields. Native statement timeout defaults
5000ms (validated1–30000ms), and connection acquisition follows request/job context.
Errors name settings rather than disclose connection values. The HTTP lifetime reserves
one session connection for PostgreSQL LISTEN; one-shot jobs reserve none. Pooling and
unsupported source overrides are listed in the [CLI guide](../services/server/cmd/social-server/README.md).

The live notification channel carries room/message identifiers, with fresh authentication
and membership checks before delivery. PgBouncer transaction pooling cannot own LISTEN;
retain a separate session connection if introducing it. Shared API/domain/catalog admission
uses PostgreSQL histories, per-identity locks, constant-time capacity accounting and bounded
expiry sweeps (ADR-0033). Account/safety/message budgets remain shared native contracts.
Admission never falls back to a process-local quota when the database is unavailable.

## Schema bootstrap and adoption

`social-server --migrate-only` installs native contracts without starting HTTP, codecs,
listeners or jobs. Fresh relational bootstrap is embedded in `internal/schema/baseline.sql`;
additional account/media/safety/messaging contracts are installed through their native
migration functions. Existing tables/user rows are adopted without dropping, resetting or
reseeding them. Preinstalled spatial extension schemas are preserved. A version marker
records applied native contracts under an advisory lock.

Production database extensions must be installed by a privileged operator first; the
serving role then needs only its application schema privileges. The local/CI PG16 Bookworm
image provides PostGIS/vector packages and permits synthetic bootstrap. This fixture role
is not a production permission recommendation.

Back up before migration, rehearse on an isolated copy, and test restore plus login,
visibility, consent and erasure behavior. Legacy sessions retire during account adoption;
users sign in again. Retain the original database/runtime for reviewed rollback. Native
bootstrap qualification covers empty, existing and spatial-preinstalled databases with
extension-owned sentinel data preserved.

## Relational, geographic and vector contracts

Native services retain the existing foreign keys, constraints, indexes and publication
fields. Geospatial predicates and nearest-neighbour ordering remain PostGIS SQL; taxonomy
edges and relational ownership stay in the same database. Deterministic recommendation
embeddings retain pgvector storage; no external model or vector database is introduced.

All user values enter parameterized queries. Dynamic identifiers are restricted to reviewed
fixed schema/field allowlists and PostgreSQL identifier quoting. Mutations run in explicit
transactions with the applicable fresh cohort/consent/block/ownership checks, audit chain
and notification chokepoint. Do not replace governed services with generic row editing.

## Deferred work and erasure

The native `internal/jobs` queue writes minimal scalar/ID payloads transactionally and
claims bounded work with row locks/SKIP LOCKED. Each handler remains idempotent and can
reload current authorization. Erasure removes relational identity and queues durable
private blob cleanup; overflow continues in bounded batches rather than discarding keys.
Safety/consent/scan gates remain on the authorization path and cannot be deferred.

## Qualification and operations

[Native testing](agent-testing.md) supplies an explicit disposable DSN and real codecs.
The canonical PG16.15 Bookworm fixture passes173 top-level tests across17 packages with
no skips. No live database values or contents belong in documentation or test receipts.
See [ASYNC_TASKS](ASYNC_TASKS.md), [RUNBOOK](RUNBOOK.md), [SCALING](SCALING.md) and
[SECURITY](SECURITY.md) for maintenance, replica limits, backup and access policy.
