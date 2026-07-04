# Production-readiness & scalability roadmap

**Code-grounded as of 2026-06-19; §0 spot-re-verified 2026-07-02** (SSRF `apps/safety/net.py`,
HNSW migration `recommendations/0002`, prod `CONN_MAX_AGE`/`statement_timeout`, Redis-flip
CACHES/CHANNEL_LAYERS all confirmed present; §2b updated — the task-queue foundation shipped
2026-06-23). Supersedes the engineering registers in `archive/AUDIT_2026-05.md`,
`archive/AUDIT_STRESS_2026-05-29.md`, and `archive/PRODUCTION_HARDENING_PLAN_2026-05.md`
(archived 2026-07-02) — most of those engineering blockers are fixed in code (verified).
Build on this + `SCALING.md` + `HOSTING_EU.md`. Feeds the repo-root `STATUS.md`.

> **Headline:** the product engine (D1–D10 + 4 feature waves) is built and tested (~1900-green
> suite), and the HTTP path is genuinely **stateless + load-balancer-ready** (DB sessions, opaque
> API tokens, `SECURE_PROXY_SSL_HEADER` + `NUM_PROXIES`, a shared-state boot guard). What's missing
> is the **operational substrate** to run it live and at scale, plus **legal sign-off**. Almost none
> of it is feature work.

## 0. Already built — do NOT rebuild

Verified present in code (a generic checklist would wrongly flag these):
Redis-backed `CACHES` + `CHANNEL_LAYERS` (flip on `REDIS_URL`, `base.py:415`) · opaque
`TokenAuthentication` + hardened/throttled token-obtain (mobile/3rd-party ready) · deny-by-default
DRF permissions · drf-spectacular + Swagger · DRF throttle scopes with XFF/`NUM_PROXIES` handling ·
request-body-size middleware · per-delivery WebSocket re-auth (4403) · SSRF safe-fetch
(`apps/safety/net.py`) · GDPR `erase_user` + export endpoints · brute-force login lockout · HNSW
pgvector ANN index · SSL-redirect + 1-yr HSTS + secure cookies · prod boot assertions (no dev IdP,
EU media residency, shared-state) · opt-in Sentry (privacy-safe) · `run_due_jobs` retention
scheduler · RO/EN localization · CI gates (ruff, migrations, `check --deploy`, pytest, docker build,
weekly pip-audit) · well-indexed hot read paths + `statement_timeout`.

Feature-level behavioral contracts (what each shipped feature guarantees and the invariant gates
it carries) live in [FEATURES_BUILT.md](FEATURES_BUILT.md) — check it before building any
"missing" feature.

---

## 1. The single biggest lever — provision shared state + the real stack

One change closes ~8 findings across scaling/security/API at once. The code is **already written**;
it's a **provisioning** gap (the shipped `render.yaml` is a free-tier *demo*).

- **Provision managed EU Redis** and set `REDIS_URL` + `DJANGO_REQUIRE_SHARED_STATE=True`. This makes
  the Channels layer cross-process (chat fan-out stops silently splitting across instances), and
  makes DRF throttles + the child-safety `allow_action` rate-limiter + the brute-force lockout
  **global and durable** instead of per-process and reset-on-restart.
- **Provision EU object storage** (`MEDIA_S3_*` — Hetzner Object Storage per `HOSTING_EU.md`). Today
  the default deploy stores blobs on the container's ephemeral disk (lost on redeploy).
- **Move off the demo `render.yaml`** to the `HOSTING_EU.md` stack (≥2 app instances, managed/HA
  Postgres, backups, Redis, object storage). Set `SENTRY_DSN` and `EUDI_TRUSTED_ISSUERS`.

---

## 2. P0 — before a real launch

### 2a. Infrastructure / availability (the SPOF cluster)
- **≥2 app instances, no SPOF** — one daphne process on one box = 100% downtime on any crash; free
  Render web also sleeps after 15 min. Run `numInstances: 2` (paid) or 2 systemd daphne units.
- **Managed / HA Postgres** — single primary for relational+geo+vector+graph; ephemeral on free tier
  and co-located with the app on the Hetzner box. Use managed EU Postgres with PITR + a hot standby.
- **Backups + a tested RESTORE drill** — backups are documented, not provisioned. Commit + schedule
  the `pg_dump`→EU-bucket script (or managed snapshots) and run one real `pg_restore` + smoke test;
  document RTO/RPO; enable bucket versioning for media.

### 2b. Async work substrate
- ~~No task queue exists~~ **DONE (2026-06-23): the foundation shipped** — a Postgres-backed
  `DeferredTask` queue (`apps/ops/tasks.py`, transactional `enqueue`, `SKIP LOCKED` claims,
  bounded retries) drained by the existing `run_due_jobs` cron. No Celery/Redis-broker by
  design — see `ASYNC_TASKS.md` + `adr/0003`. **Updated 2026-07-04:** production kinds are now
  registered for blob cleanup, activity notification fan-out, bounded notification retention,
  allowlisted cron-command splitting, and a fail-closed media-scan placeholder.
- Remaining async moves: a real withheld-state media scanner (strict timeout/circuit breaker),
  full GDPR export/erasure orchestration beyond blob cleanup, broadcast group_send, and converting
  the serial daily cron call sites to enqueue `cron.run_command` tasks where useful.

### 2c. Observability (operate-it-live basics)
- **Structured logging + request/correlation IDs — DONE (2026-07-04)**: `X-Request-ID` is minted or
  safely propagated, echoed on responses, added to log records, tagged on the Sentry scope, and
  emitted in PII-safe request logs when `REQUEST_LOGGING_ENABLED` is on. `LOG_FORMAT=json` switches
  to JSON lines; production defaults to JSON while dev/test stay quiet/readable.
- **Wire Sentry at deploy** (`SENTRY_DSN` on web *and* cron) + capture **periodic-job failures**
  (a silently failing `consent_renewal_sweep`/`purge_messaging` is a GDPR/safety miss) + a cron
  check-in monitor for missed nightly ticks. Add the Channels integration for WS exceptions.
- **Alerting + uptime/SLO** — external uptime monitor on `/healthz` + Sentry alert rules; write a
  one-line SLO.

### 2d. Edge security
- **Edge protection (WAF / DDoS / edge rate-limit)** — all abuse mitigation is inside the single
  process today. Put **Cloudflare** (free, EU options) or the provider WAF in front: blanket edge
  rate-limit on `/api/auth/token/` + login, managed OWASP ruleset, bot/DDoS mitigation. Biggest
  single infra-security gap for a public launch.
- **Durable rate-limit / lockout** — same fix as §1 (Redis), called out because without it
  brute-force/credential-stuffing protection is effectively weak on a multi-process or
  cold-starting deploy; also make the safety limiter `incr` atomic (NX-init + incr).

### 2e. Legal / external (not code — gating the *child-first* mission)
- **DPIA / ROPA / breach runbook exist as drafts** — need DPO appointment, RO-counsel sign-off, and
  signed processor DPAs (Render/Hetzner/object-storage/Sentry).
- **Live EUDI / national trust anchor** — minors stay structurally OFF (`ALLOW_MINOR_ONBOARDING`
  False) until a real RO wallet issuer exists (~Dec 2026) + the DPIA is signed. **Plan an
  adults-only launch**; the guardian link is still a mutual-click, not verified parental authority.
- **Lawful CSAM scanner** — the fail-closed default keeps photo uploads OFF (correct). Before any
  minor-cohort photos: wire a lawful perceptual matcher (PhotoDNA-class) via the `ManagedScanner`
  seam + a reporting obligation, with legal authorisation captured in the DPIA.

---

## 3. P1 — to scale (and harden)

### API / DRF contract
- **API versioning — DONE (2026-07-04)**: canonical `/api/v1/`, transitional `/api/` alias, DRF
  `URLPathVersioning`, and OpenAPI filtering that documents only the versioned API paths.
- **Pagination bounds — DONE (2026-07-04 for app APIViews)**: global bounded limit/offset plus
  cursor/limit envelopes on `/api/v1/` discovery feeds, activity thread reads, social list actions,
  messaging conversation/history reads, and notification lists. The `/api/` alias keeps legacy
  response shapes while still retaining existing hard caps.
- **N+1 CI guard — PARTIAL (2026-07-04)**: existing `participant_keys()` guard remains; added
  query-count guards for v1 thread reads, notification list reads, messaging conversation/history
  reads, and social membership list reads. Broader prod-sized `EXPLAIN`/index work remains
  deferred until there is representative traffic.

### Horizontal scaling (after Redis)
- **Multiple workers behind the LB** (uvicorn-workers/multi-replica to use all cores).
- **Media egress off the app process** — every blob (≤7 MiB) is buffered + streamed through the
  single daphne loop. Add `presigned_get_url` to the S3 backend and **302/307-redirect after the
  membership check** so bytes never transit the app (the `SCALING.md` #1 item); optionally CDN-front.
- **PgBouncer** (transaction pooling) before scaling past one process; set `CONN_MAX_AGE=0` +
  disable server-side cursors when pooling.

### Database over time
- **Notification covering index — DONE (2026-07-04)**: `notifications/0017` adds
  `(recipient, -created_at)` with `AddIndexConcurrently` / `atomic=False` for inbox reads.
- **Notification retention — DONE (2026-07-04)**: `notifications.retention_purge` deletes one
  bounded batch of old read mutable notices; unread and MODERATION/SYSTEM safety/DSA notices are
  excluded.
- **Audit chain verification — DONE (2026-07-04)**: `verify_audit_chain()` streams with
  `.iterator()` and `verified_audit_checkpoint()` returns a verified high-water mark for incremental
  extension checks. Periodic full verification remains the way to re-check old history.
- **High-growth tables** (Post, AuditLog) — plan declarative monthly RANGE partitioning before tens
  of millions of rows.
- **Read-replica routing** (env-gated `REPLICA_DATABASE_URL` + a router for public read-only GETs).
- **Zero-downtime migration discipline — PARTIAL**: hot-table index builds now have a concrete
  `AddIndexConcurrently`/`atomic=False` example; no migration-linter dependency or CI seam exists
  yet, so automated zero-downtime migration linting remains open.

### Reliability
- **Retries / circuit-breakers** for Stripe + booking (the Overpass/scanner pattern, applied
  consistently) so a transient 5xx isn't a 500 that pins a worker for 15s.
- **`/readyz` — PARTIAL (2026-07-04)**: `/healthz` is pure liveness; `/readyz` checks DB plus
  Redis/storage only when configured and returns degraded booleans without backend details.
  Remaining: graceful shutdown drain that flips `/readyz` to 503 on SIGTERM for zero-downtime once
  ≥2 instances.
- **Operational metrics** — django-prometheus `/metrics` (request latency/status, DB timing, live WS
  gauge) scraped by a free Grafana/Prometheus.

### Security hardening
- **CSP enforcement switch — HARDENED (2026-07-04)**: django-csp uses one shared policy in
  report-only by default; key SSR pages have executable inline scripts/event handlers and practical
  inline style attributes/blocks extracted to static CSS/JS, JSON script islands are nonced, the
  shared policy no longer includes `style-src 'unsafe-inline'`, and `DJANGO_CSP_ENFORCE=True` flips
  the same policy to `Content-Security-Policy` after production violation reports are reviewed.
  Operators can group exported report-only payloads with `digest_csp_reports`; remaining review is
  to fix any deployed violations from pages outside the CSP smoke set before enforcing.
- **Explicit security headers** — pin `SECURE_CONTENT_TYPE_NOSNIFF`/`SECURE_REFERRER_POLICY`/COOP +
  a `Permissions-Policy` (lock camera/mic, scope geolocation to self).
- **SAST + container scanning** in CI — CodeQL/Semgrep (Django ruleset) + Bandit + Trivy/Grype on the
  built image (fail on HIGH/CRITICAL).
- **Container non-root** (matters on the self-hosted Hetzner path) + read-only rootfs.
- **PDF/ClamAV** scanner wired before scaling adult PDF sharing.
- **Independent pen test** before public beta (close the cheap items above first).

### Deploy / CI-CD
- **Gate deploys on green CI** — Render autoDeploys on git push regardless of CI status; disable
  autoDeploy + trigger via a deploy hook from a passing CI job (or an SSH/rsync deploy workflow on
  Hetzner).
- **Staging environment** — there is exactly one env (prod); migrations + the fail-closed boot
  guards are first exercised in production.
- **IaC (Terraform + cloud-init/Ansible)** for the recommended Hetzner box so a rebuild is one
  command, not a prose runbook.
- **CDN for static + media**; **harden the cron's own SPOF** with a dead-man's-switch heartbeat
  (missed nightly run = GDPR/DSA compliance miss).

---

## 4. Recommended sequence

1. **Provision shared state + real stack** (§1) — Redis, object storage, ≥2 instances, managed
   Postgres + backups/restore drill, Sentry DSN. *Unlocks the most, all config not code.*
2. **Edge: Cloudflare WAF/rate-limit** in front (§2d).
3. **Add the task queue** (§2b) + move scanning/erasure/fan-out/broadcast off-request.
4. **Observability**: structured logging + request IDs, Sentry on jobs, uptime/alerts, `/readyz` (§2c).
5. **API v1 + pagination bounds + N+1 CI guard** (§3) before any native client.
6. **Scale levers as traffic grows**: media presigned-redirect, PgBouncer, replica routing,
   partitioning/retention, metrics.
7. **Security hardening + pen test** (§3) before public beta.
8. **Legal sign-off + (later) live EUDI anchor** for the minor-onboarding mission (§2e).

> **Adults-only beta** is reachable with §1–§4 + legal sign-off. The **child-first** mission is
> gated on external dependencies (EUDI anchor + DPIA), not on code.
