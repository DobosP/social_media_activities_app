# Ops & incident-response runbook (D9)

Operational guide for running the service in production. Pairs with
[RELEASE_READINESS](RELEASE_READINESS.md), [SECURITY](SECURITY.md), [SAFETY](SAFETY.md).

## Deployment

- **Topology (beta, one city):** single EU VPS → managed EU Postgres (PostGIS) as load
  grows; object storage (R2/MinIO) for media blobs; CDN for static assets.
- **App server:** ASGI via `daphne config.asgi:application` (the `Dockerfile` default) so
  WebSocket chat works. Behind TLS-terminating reverse proxy; `SECURE_PROXY_SSL_HEADER`
  is set in `config/settings/prod.py`.
- **Channels layer:** set `CHANNEL_LAYER_BACKEND` to a Redis layer (channels-redis) for
  multi-process/multi-node deploys; the in-memory default is single-process only.
- **Required env:** `DJANGO_SECRET_KEY`, `DJANGO_ALLOWED_HOSTS`, `DATABASE_URL`,
  `IDENTITY_PROVIDER` (+ provider creds), `MEDIA_STORAGE_BACKEND` (+ bucket creds),
  `DONATIONS_PROVIDER` (+ `DONATIONS_CHECKOUT_BASE_URL`).
- **Migrate on deploy:** `python manage.py migrate` (custom user model → fresh DBs need it).

## Health & monitoring

- **Liveness/readiness:** `GET /healthz` (checks DB). Wire to the load balancer + uptime check.
- **Aggregate metrics:** `GET /api/ops/stats` (staff-only, aggregate-only — no PII, no
  behavioural tracking, per IS-6). Do **not** add per-user analytics.

## Backups & restore

- **Postgres:** nightly automated dumps (managed-DB snapshots preferred); retain ≥30 days;
  **test restore quarterly**. Restore: provision DB, `pg_restore`, run `migrate`, smoke-test
  `/healthz` and a login.
- **Object storage:** enable versioning + lifecycle on the media bucket.

## Safety / incident response

1. **Detect** — report queue (`apps/safety` admin), CI/security alerts, abuse signals.
2. **Triage** — severity. **CSAM / child-safety = highest:** preserve evidence, do not
   download/redistribute; the upload pipeline already blocks known hashes and audit-logs.
3. **Contain** — `take_action` (suspend/ban) deactivates accounts; remove content; the
   hash-chained `AuditLog` records every safety action (`verify_audit_chain()` proves
   integrity).
4. **Escalate** — legal/LEA reporting where required (CSAM, credible threats); notify DPO
   for personal-data incidents (GDPR 72-hour breach clock).
5. **Recover & review** — restore service, post-incident write-up, fix root cause.

## Routine maintenance

- **Chat retention:** `python manage.py purge_chat` (honours `CHAT_RETENTION_DAYS`); schedule
  if a retention window is set.
- **Dependency hygiene:** Dependabot + weekly `pip-audit` (CI); bump deliberately per
  [SECURITY](SECURITY.md).
- **Donations reconciliation:** provider webhook → `donations.services.complete_donation`.

## Cost controls

- Single VPS + managed Postgres to start; object storage is pay-per-use; CDN caches static.
- No ad/tracking infrastructure to run. Scale Postgres/Redis only as metrics justify.
