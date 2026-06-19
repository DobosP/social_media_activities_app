# Production-readiness & scalability roadmap

**Code-grounded as of 2026-06-19.** Supersedes the engineering registers in `AUDIT_2026-05.md`,
`AUDIT_STRESS_2026-05-29.md`, and `PRODUCTION_HARDENING_PLAN_2026-05.md` — most of those
engineering blockers are now fixed in code (verified). Build on this + `SCALING.md` + `HOSTING_EU.md`.

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

### 2b. Async work substrate (the one genuinely-missing code component)
- **No task queue exists** (no Celery/Dramatiq/RQ/django-q anywhere). Add one — given the
  Postgres-primary, no-Redis-by-default design, prefer **django-tasks (Django 5.2 native)** or
  **procrastinate/django-q2 on the existing Postgres**, or Celery+Redis once Redis is provisioned.
- Then move off the request/socket thread: **media safety scanning** (bound it with a strict
  timeout + circuit breaker + a quarantined `pending_scan` state), **GDPR erasure/export** (unbounded
  cascade can hit the request timeout → partial erasure = legal risk), notification fan-out, and the
  broadcast group_send. Convert the serial daily cron into enqueued tasks (parallel + per-job retry +
  dead-letter).

### 2c. Observability (operate-it-live basics)
- **Structured logging + request/correlation IDs** — today: plain text, WARNING+ only, no JSON, no
  way to trace a request across HTTP + WS + audit. Add a `LOGGING` dict + a request-id middleware
  (propagate into Sentry scope; keep PII-free).
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
- **API versioning** — no `/v1`, no `DEFAULT_VERSIONING_CLASS`. Add `URLPathVersioning` (mount under
  `/api/v1/`, keep a transitional alias) *now*, before a native client ships against the contract.
- **Pagination bounds** — `LimitOffset` has no `max_limit` (a client can request `?limit=50000`) and
  the highest-traffic discovery feeds are hard-sliced `APIView`s with **no pagination at all**. Set a
  `max_limit` default + give discovery/thread feeds a cursor paginator.
- **N+1 CI guard** — fix `participant_keys()` (one query/participant) and add an
  `assertNumQueries`/nplusone test around the top list endpoints so regressions fail the build.

### Horizontal scaling (after Redis)
- **Multiple workers behind the LB** (uvicorn-workers/multi-replica to use all cores).
- **Media egress off the app process** — every blob (≤7 MiB) is buffered + streamed through the
  single daphne loop. Add `presigned_get_url` to the S3 backend and **302/307-redirect after the
  membership check** so bytes never transit the app (the `SCALING.md` #1 item); optionally CDN-front.
- **PgBouncer** (transaction pooling) before scaling past one process; set `CONN_MAX_AGE=0` +
  disable server-side cursors when pooling.

### Database over time
- **Notification covering index** `(recipient, -created_at)` (cheap; the inbox sort currently can't
  use the `(recipient, read_at)` index).
- **High-growth tables** (Post, Notification, AuditLog) — add a Notification retention purge (read
  notifications aren't safety records); plan declarative monthly RANGE partitioning of AuditLog/Post
  before tens of millions of rows.
- **`verify_audit_chain` full-table scan** — stream with `.iterator()` + a verified high-water
  checkpoint.
- **Read-replica routing** (env-gated `REPLICA_DATABASE_URL` + a router for public read-only GETs).
- **Zero-downtime migration discipline** — expand/contract convention +
  `AddIndexConcurrently`/`atomic=False` for index builds on hot tables + a CI migration linter.

### Reliability
- **Retries / circuit-breakers** for Stripe + booking (the Overpass/scanner pattern, applied
  consistently) so a transient 5xx isn't a 500 that pins a worker for 15s.
- **`/readyz`** (checks DB/Redis/storage when configured; `/healthz` stays pure liveness) +
  **graceful shutdown** (drain on SIGTERM, flip `/readyz` to 503) for zero-downtime once ≥2
  instances.
- **Operational metrics** — django-prometheus `/metrics` (request latency/status, DB timing, live WS
  gauge) scraped by a free Grafana/Prometheus.

### Security hardening
- **CSP** (django-csp, report-only → enforce; tighten for Leaflet + the chat aria-live region) — the
  highest-value missing browser-side control for a child-facing UI.
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
