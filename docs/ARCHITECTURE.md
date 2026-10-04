# Architecture

Verified against the native implementation on 2026-10-04. Runtime selection is recorded
in [ADR-0032](adr/0032-complete-native-go-backend.md); current activation and launch gates
remain in [STATUS](../STATUS.md). Read [FEATURES_BUILT](FEATURES_BUILT.md) before adding
an existing capability again.

## Principles

- PostgreSQL is the primary datastore: relational state, the activity graph, PostGIS
  geography and pgvector similarity share one authoritative database. Private object
  storage holds media bytes; database rows decide access.
- Domain services share safety, consent, cohort and blocking rules across API, HTML and
  live transport. Identity assurance minimizes stored data to necessary verification
  results and age bands; signing in does not verify age.
- License, provenance, confidence and source credits survive ingestion, deduplication,
  enrichment and media transformations. Confirmed/manual contributions are protected.
- Producer, serving layer and consumers communicate through reviewed HTTP/data
  contracts. A consumer does not import a producer's implementation.
- Third-party identity, booking, donations, scanners and data sources have typed native
  boundaries. A configured provider is not permission to activate it or ingest data.

## Serving implementation

[services/server](../services/server/) is a modular Go executable. Its `internal/app`
constructor connects the domain services and registers API, HTML, staff, operations and
WebSocket routes. The frontend remains the Preact/Vite TypeScript client; release assets,
templates, locale and reviewed reference data accompany the executable.

| Native package | Responsibility |
|---|---|
| `accounts`, shared `services/authcore` | Authentication, sessions/CSRF, age assurance, guardians, consent and erasure |
| `catalog` | Taxonomy, places, events, provenance and contribution/review workflows |
| `social` | Activities, groups, threads, memberships, connections and communities |
| `safety`, `platform` | Reports, blocking, governed moderation, hash-chained audit and notification gates |
| `media` | Fail-closed admission, bounded codecs, private serving and durable byte cleanup |
| `messaging`, `chat` | E2EE direct/group state and live transport over authorized domain state |
| `booking`, `donations` | Typed provider transitions and audited ledger state |
| `discovery`, `recommendations`, `notifications` | Gated discovery, similarity and inbox surfaces |
| `jobs`, `commands`, `ops` | Bounded explicit jobs, operator commands, deferred queue, health and aggregate metrics |
| `web`, `admin` | Native HTML/SPA context and curated audited staff operations |

The shared authentication implementation is canonical in Cat's `shared-go/authcore`.
Social's portable copy has hash-pinned provenance; production does not depend on a
sibling repository checkout. Native administration permits curated edits and governed
transitions, with raw age/consent/cohort, membership, ciphertext, scanner and payment
state outside generic CRUD. See [NATIVE_SERVER](NATIVE_SERVER.md) for qualification and
intentional restrictions.

`apps/`, `config/` and the reference image preserve Django contracts for offline tests.
They are not serving, proxy or worker dependencies of the native release.

## Graph and geography

Category/type parent references express the activity hierarchy. Typed activity relations
express lateral `related`, `synonym`, `variant` and `requires` edges. Place-to-activity
edges carry inferred/confirmed/manual origin, confidence and mapping provenance. Native
catalog, discovery, recommendations and activity creation traverse these relational
contracts rather than a separate graph database.

Place geography uses SRID 4326 and GiST indexing; geography distance is measured in
metres. The places API retains bounded nearby/radius/bounding-box queries. Native source
adapters normalize reviewed OSM/Overture/RO-EDU and enrichment inputs before idempotent
catalog updates; source keys, credits and protected manual edges survive each update.
Real network ingestion remains an explicit operator action.

## Runtime and lifetime

The executable serves HTTP and WebSockets directly, with a process-local `pgxpool`:
minimum zero and maximum four connections by default. The configured maximum is bounded
to two through four; SQL statement timeout is 5 seconds by default. The live broker
reserves one connection while listening for PostgreSQL ID-only notifications. It reloads
authoritative state before delivery; NOTIFY does not carry messages or permission.

API/social/catalog/saved-search/CSP admission uses shared PostgreSQL histories and capacity
counters (ADR-0033); authentication/account/safety/message budgets remain database-backed.
Reviewed policy overrides and bounded privacy-safe Sentry are native (ADR-0034); unsafe
floors, retired Python runtime controls and unused Redis settings are refused. Guarded
permissions/private schemas and fresh participation are ADR-0035/0036 review extensions.
See [SCALING](SCALING.md) before selecting a multi-process deployment.

Schema bootstrap/adoption runs through `social-server --migrate-only`, retaining existing
rows and preinstalled extension schemas. Legacy sessions retire on migration. HTTP startup
checks required codecs but starts no recurring scheduler or ingestion. A committed private
video upload may trigger one bounded single-flight pass of at most two queued videos;
explicit jobs/timers provide recovery. Shutdown drains HTTP, cancels/waits for background
media, and releases live/storage/database resources in order.

## Working conventions

These five rules apply to every native surface:

1. Put domain logic in `services/server/internal/<domain>`. API, HTML, staff and live
   adapters call the same governed service; views/templates do not bypass authorization.
2. Commit each state-changing domain operation, its audit and related row changes in a
   `pgx.Tx`. Record audit through `platform.RecordAudit` inside that transaction. Private
   prepared artifacts publish with their owning row transaction and retain durable
   cleanup when that transaction fails.
3. Route in-app notification creation through `platform.Notify`. Preserve current
   mute/block checks and the non-mutable safety/DSA carve-outs; deferred fan-out rechecks
   current recipients when it runs.
4. Register periodic work in `internal/jobs`; `jobs.DueNames` is the 27-job fan-out
   formerly named `DUE_JOBS`. `social-server --due` runs one bounded pass and exits.
   Scheduling and live-source activation require their own explicit authorization.
5. Recheck cohort, age/consent, membership, blocking, privacy and moderation at the domain
   boundary and before private delivery. Cached IDs, processed bytes, tokens and live
   subscriptions never replace current authoritative permission.

Deployment instructions: [HOSTING_EU](HOSTING_EU.md), [RUNBOOK](RUNBOOK.md),
[FILE_STORAGE](FILE_STORAGE.md), [ASYNC_TASKS](ASYNC_TASKS.md). The complete earlier
Django architecture is retained as [historical reference](archive/architecture-native-go-reference.md).
