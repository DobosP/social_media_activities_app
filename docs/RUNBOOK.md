# Ops & incident-response runbook (D9)

Operational guide for running the service in production. Pairs with
[RELEASE_READINESS](RELEASE_READINESS.md), [SECURITY](SECURITY.md), [SAFETY](SAFETY.md).

## Deployment

- **Topology (beta, one city):** single EU VPS → managed EU Postgres (PostGIS) as load
  grows; object storage (Hetzner Object Storage, EU — see [HOSTING_EU](HOSTING_EU.md);
  minors' media never goes to R2, MinIO is banned org-wide) for media blobs; CDN for
  static assets.
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

### Account sanctions — durations a moderator must understand

`take_action` (and the DRF `…/moderation/reports/<id>/resolve/` endpoint) offers three
account-deactivating sanctions. The **duration semantics are not interchangeable**:

| Sanction | Duration | Auto-lifts? | On the identity ban-ledger? |
|---|---|---|---|
| `SUSPEND` **with** days | until `expires_at` | **Yes**, by the nightly `lift_suspensions` job | No |
| `SUSPEND` **without** days | indefinite (`expires_at = NULL`) | **No — never** | No |
| `TIMED_BAN` (days **required**) | until `expires_at` | **Yes** | No |
| `BAN` | lifetime | No | **Yes** (`BannedIdentity`, survives GDPR erasure, blocks wallet re-registration) |

- A **`TIMED_BAN` always requires a duration** — the API rejects one without `suspend_days`
  (400) so it can never silently become a never-lifting deactivation.
- A **`SUSPEND` with no duration is a *permanent* deactivation** that the auto-lift job will
  **never** touch (there is no expiry to elapse). It is reversible only by a manual lift or an
  overturned appeal, and it is **not** recorded on the `BannedIdentity` ledger — so a banned
  wallet could still re-register a new account. **If you intend a permanent, ban-evasion-proof
  removal, use `BAN`, not an open-ended `SUSPEND`.**

### Authority referrals — the ledger is internal; the report-out is on you

`create_authority_referral` / `AuthorityReferral` is an **internal, tamper-evident ledger
only**. It records *that* a referral was decided and pins it to the hash-chained `AuditLog`
(`referral_proof_pack` produces the lawful-request bundle, and reading it is itself audited).
**It does not transmit anything to any external authority.** The actual out-of-band report is a
manual duty:

- **CSAM** → report to the national hotline / **INHOPE** member and, where mandatory, law
  enforcement **without delay** (treat as the highest priority; preserve evidence, never
  download/redistribute). Romania: **IGPR** (Poliția Română) and the relevant hotline.
- **Credible threats / grooming** → law enforcement per local obligation.
- Record the external case/reference number back into the referral's `reference` field so the
  internal ledger and the real-world report line up.
- The subject is **deliberately not notified** (tipping off a suspect can defeat an
  investigation); any account sanction applied alongside still carries its own DSA Art.17 notice.

> Operational SLA: a referral row with no external report filed is an open compliance task.
> Until an external transmission integration exists, the on-call moderator owns sending the
> out-of-band report within the legally-required window and recording its reference.

## Routine maintenance

- **Chat retention:** `python manage.py purge_chat` (honours `CHAT_RETENTION_DAYS`); schedule
  if a retention window is set.
- **Dependency hygiene:** Dependabot + weekly `pip-audit` (CI); bump deliberately per
  [SECURITY](SECURITY.md).
- **Donations reconciliation:** provider webhook → `donations.services.complete_donation`.

## Cost controls

- Single VPS + managed Postgres to start; object storage is pay-per-use; CDN caches static.
- No ad/tracking infrastructure to run. Scale Postgres/Redis only as metrics justify.
