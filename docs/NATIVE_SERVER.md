# Native Go server

Current status: [STATUS](../STATUS.md). Selection and boundaries:
[ADR-0032](adr/0032-complete-native-go-backend.md). The owner approved the complete native implementation for main on 2026-10-04.
Code landing does not activate product/provider/minor launch.

## Serving and data

`services/server/cmd/social-server` assembles native account, domain, safety, catalog,
recommendation, discovery, notification, booking, donation, media, operations, HTML/SPA
and WebSocket handlers. It executes no Python/Django server, worker or upstream proxy.
Client-side TypeScript and encryption remain in the existing frontend. The original
Django sources and `Dockerfile.reference` are offline migration/contract oracles.

Fresh bootstrap and existing-data adoption use `social-server --migrate-only` with
explicit `DATABASE_URL`. Back up the database first and rehearse migration and rollback
on an isolated copy before a real rollout. Existing rows/password hashes/provider IDs
remain; old sessions retire and users sign in again. Preinstalled PostGIS schemas are
adopted without recreation. No command drops, resets or reseeds an existing database.

`docker compose up --build` is synthetic loopback development; the database has no
published host port. Optional seeded data is an explicit `--profile demo run --rm seed`.
Migration-only runs without codec/auth/provider startup. Serving and jobs require bounded
FFmpeg/prlimit, ffprobe for enabled video and avifenc for AVIF output, plus approved scanners
and private storage. Missing tools/providers fail closed rather than producing unsafe uploads.

## Accounts and operations

The hash-pinned [shared authentication core](../services/authcore/README.md) supports
username/password and configured Google/Facebook authorization-code login. Provider
registration/callback acceptance still requires actual registered-app integration testing;
fixtures prove signatures, state, nonce, PKCE and single-use behavior only. Login never
sets age, consent, cohort or parental assurance. EUDI/guardian verification owns those gates.

The [CLI guide](../services/server/cmd/social-server/README.md) lists typed environment
settings, exact unsupported overrides and one-shot commands. The [manual registry](../services/server/internal/commands/README.md)
and [guarded administration](../services/server/internal/admin/README.md) document ingestion,
maintenance, curated edits, bootstrap and native audited transitions. Secrets enter through
the fleet SOPS delivery path or private operator stdin, never arguments or logs. Scheduled
jobs/real ingestion/providers/minors are not automatically enabled by the port.

## Deployment and scaling

The Docker image is Go-only, UID10001, with immutable compiled frontend/templates/locales.
Compose, Render and systemd entry points invoke the same native binary. The unapplied
cloud-init template installs an immutable artifact with an exact SHA-256; it builds no
application code on the small host. Export with `docker build --target release --output
 type=local,dest=<task-scratch>/release .`, archive that directory and record its SHA-256.
The host also needs the listed native codec packages. Terraform remains unapplied and
requires owner procurement/launch authorization.

Set `SITE_BASE_URL` and `DJANGO_ALLOWED_HOSTS` to the canonical production origin. A TLS
reverse proxy must have its actual network in `TRUSTED_PROXY_CIDRS`; arbitrary XFF/XFP
headers are ignored. The image liveness probe uses the canonical Host and local-only
`/healthz` HTTP allowance; readiness tests database/configured dependencies and draining.
Request logs contain bounded route patterns/status/duration/request ID, without raw private
paths, queries, IPs, identities, credentials or bodies.

A small PostgreSQL pool (default4; one live LISTEN connection) bounds database work.
OAuth state/attempts, tokens, API/social/catalog/saved-search/CSP admission, account/safety/
message budgets, job queues and ID-only live fanout use PostgreSQL. Shared sliding admission
uses per-identity locks and constant-time capacity counters, with bounded expiry cleanup;
anonymous peer keys are keyed hashes and account-linked rows cascade on erasure. Missing
schema or unavailable admission refuses work. [ADR-0037](adr/0037-postgresql-shared-rate-budgets.md)
records the storage and retry contract; LISTEN still needs a session connection with PgBouncer.
No measured heap or fixture latency is a hosting-price/capacity guarantee.

The CLI maps reviewed typed overrides to their domain policies; unsafe floors, obsolete
Python runtime knobs and unused Redis configuration are refused by name. Required-shared
mode uses the native PostgreSQL contract. Optional Sentry sends bounded fixed error classes
and coarse route labels, with no raw errors/requests/identities or tracing. Configuration,
privacy and shutdown boundaries: [ADR-0034](adr/0034-native-config-error-observability.md).
Production reporting/provider acceptance remains an explicit operations gate.

[Guarded permission management](../services/server/internal/admin/README.md) uses fixed
presets, fresh eligible manager/target authority, atomic audit and credential revocation;
it cannot grant age/cohort/consent or bypass domain services. Private OpenAPI now describes
all380 registered API operations over318 paths with166 field schemas, including actual
DTOs, SQL projections, binary media, status/security and query/body contracts. Coverage
checks compare against native registrations and representative wire values.
[ADR-0035](adr/0035-guarded-permissions-private-schema.md) records those boundaries;
[ADR-0036](adr/0036-fresh-participation-authority.md) records fresh participation authority.
Qualification commands: [agent-testing](agent-testing.md); [completion receipt](reviews/native-go/completion-qualification.md); current review gates:
STATUS/WORKLOG. Completion extensions remain a review candidate until human approval.
