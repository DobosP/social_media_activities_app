# EU hosting design — cheapest credible, data-resident, child-safety-first

> Scope: where and how to run this nonprofit, open-source, text-first platform for its
> first launch city, **Cluj-Napoca, Romania (EU)**. Every recommendation here is grounded
> in the repo's actual config (`render.yaml`, `Dockerfile`, `config/settings/*.py`,
> `apps/media/storage.py`, `.env.example`, `requirements.txt`).
>
> Hard constraints this doc must satisfy (from `CLAUDE.md` / `docs/SAFETY.md`):
> child data and media **never leave the EU**; **privacy by default**; **no per-user
> cloud-AI spend**; **cheap + open-source** (donations-funded). The stack is
> Django 5.2 + DRF + GeoDjango/PostGIS + `pgvector` (one Postgres primary), ASGI/daphne
> with Channels WebSockets (needs Redis cross-process), and S3-compatible object storage
> for private photos/PDFs served only via signed, expiring, per-viewer URLs.

---

## 1. TL;DR recommendation

**Run everything on one small Hetzner Cloud box, plus Hetzner Object Storage for blobs and backups.**

| Component | What | Where | Cost |
|---|---|---|---|
| Compute | 1× Hetzner Cloud **CPX21** (3 vCPU / 4 GB / 80 GB NVMe) | Nuremberg/Falkenstein (DE) or Helsinki (FI) | **~€7.99/mo** |
| Database | PostgreSQL 16 + PostGIS 3 + `pgvector`, **co-located** (apt packages) | same box | €0 |
| Cache + Channels | Redis, **co-located** (apt package) | same box | €0 |
| App server | `daphne` (ASGI) behind Caddy/nginx for TLS | same box | €0 |
| Object storage | Hetzner **Object Storage** (S3-compatible, EU; private blobs + `pg_dump` target) | DE/FI | **~€4.99/mo** (incl. 1 TB egress, then ~€1/TB) |
| **Total** | | **all EU-resident, EU-owned** | **≈ €13/mo** |

This is the cheapest credible stack that keeps **all** minor data — relational, geospatial,
vector, *and* media blobs *and* backups — inside EU borders on an **EU-owned** provider, with
**zero** per-user AI cost. Hetzner is a German company with EU datacenters (Germany, Finland),
so there is no US-processor / Schrems-II transfer question to paper over with SCCs.

**Even cheaper:** the **CX22** shared-vCPU box (~€4.49/mo, 2 vCPU / 4 GB) brings the total to
**≈ €9/mo**. CX is fine to launch on; CPX (dedicated-ish AMD vCPU) gives steadier latency once
WebSocket chat is active. Start on CX22 if you are pinching pennies, size up to CPX21 when CPU
steal becomes visible.

> The full product engine is deliberately **deterministic / no-ML** (thread digests, draft
> text, communities are template/predicate-based — see `FEATURES_BUILT.md`), so a 2-vCPU/4-GB box with
> co-located Postgres comfortably serves a single-city launch. No worker fleet, no Kubernetes.

---

## 2. Why this over a PaaS

The repo already ships `render.yaml` (`region: frankfurt`) — the **lowest-ops** option. Use it
if you value "git push and forget" over cost. But it (and every other managed PaaS) is
**4–6× pricier** and **US-owned**, which adds a transfer-compliance burden for minors' data:

| Option | Realistic monthly | Owner / jurisdiction | Note |
|---|---|---|---|
| **Hetzner single box** (this doc) | **~€13** | DE (EU-owned) | Cheapest; no US processor |
| Render (Frankfurt) | **$50–80** | US | web + worker/cron + Postgres + Redis add up fast; free tier sleeps + ephemeral DB |
| Fly.io (AMS/CDG/etc.) | from **$38** just for Managed Postgres | US | per-resource billing; Redis (Upstash) extra |
| Railway (EU metal) | **$20/vCPU-mo + $10/GB-RAM-mo** + DB/Redis | US | usage-priced; easy to overshoot |

**Convenience alternative — Render-Frankfurt.** The blueprint is already wired (web on `daphne`,
Postgres in Frankfurt, a paid `cron` for `run_due_jobs`, secrets via the dashboard). The trade-offs:

- **US processor.** Render Inc. is US-owned → you need a **signed DPA + SCCs** and a transfer
  assessment for minor data, even though the *region* is Frankfurt.
- **Verify the extensions before committing.** GeoDjango auto-enables **PostGIS** during
  `migrate` (the blueprint relies on this), but **`pgvector` is a separate extension** —
  confirm Render's managed Postgres 16 image ships `pgvector` (the `recommendations`/discovery
  app needs it) *before* you depend on Render's DB. If it doesn't, you'd have to run Postgres
  yourself anyway, which removes most of Render's convenience.
- **Free tier is a demo, not a launch:** the web service sleeps after ~15 min idle and the free
  Postgres has a limited lifespan (`render.yaml` says so). A real launch is the paid tier → the
  $50–80 figure above.

**Bottom line:** Hetzner single box for the real launch; Render-Frankfurt only if you'll trade
~5× the money for not running a server.

---

## 3. Concrete single-box recipe (Hetzner)

A copy-paste path from a blank Ubuntu 24.04 Hetzner box to a running, TLS-terminated app with
co-located Postgres + Redis. No Kubernetes, no Docker required on the host (a `docker-compose`
variant is given at the end if you prefer containers).

### 3.0 Provision

1. Hetzner Cloud console → **New Server** → location **Falkenstein/Nuremberg (DE)** or
   **Helsinki (FI)** (both EU). → image **Ubuntu 24.04** → type **CPX21** (or CX22).
2. Add your SSH key; enable the cloud firewall (allow 22/tcp from your IP, 80+443/tcp from all).
3. Create a **Hetzner Object Storage** bucket in the **same EU region**; generate an S3
   access key/secret (used below for `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`).

### 3.1 System packages (Postgres + PostGIS + pgvector + Redis + GeoDjango libs)

```bash
sudo apt-get update
sudo apt-get install -y \
  postgresql-16 postgresql-16-postgis-3 postgresql-16-pgvector \
  redis-server \
  python3.12 python3.12-venv python3-pip \
  binutils gdal-bin libgdal-dev libgeos-dev libproj-dev \   # GeoDjango native libs (mirror Dockerfile)
  curl gnupg awscli

# Caddy (TLS reverse proxy) is NOT in Ubuntu's default repos — add its apt repo first:
curl -1sLf -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg.key https://dl.cloudsmith.io/public/caddy/stable/gpg.key
sudo gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg /usr/share/keyrings/caddy-stable-archive-keyring.gpg.key
curl -1sLf https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt | sudo tee /etc/apt/sources.list.d/caddy-stable.list
sudo apt-get update && sudo apt-get install -y caddy
```

> The two database extension packages — **`postgresql-16-postgis-3`** and
> **`postgresql-16-pgvector`** — are exactly what the app needs. GeoDjango enables the
> `postgis` extension automatically during `migrate`; create the `vector` extension once
> (below) for the `recommendations`/discovery app.

### 3.2 Create the database role + DB

```bash
sudo -u postgres psql <<'SQL'
CREATE ROLE app LOGIN PASSWORD 'CHANGE_ME_STRONG';
CREATE DATABASE app OWNER app;
\c app
CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS vector;   -- pgvector
SQL
```

Keep Postgres and Redis bound to **localhost only** (the defaults) — they never need a public
port; the app talks to them over the loopback.

### 3.3 App user, code, venv

```bash
sudo useradd --system --create-home --shell /usr/sbin/nologin app
sudo -u app git clone <this-repo> /home/app/social_media_activities_app
cd /home/app/social_media_activities_app
sudo -u app python3.12 -m venv .venv
sudo -u app .venv/bin/pip install -r requirements.txt   # daphne, channels, channels-redis, boto3, pgvector all here
```

### 3.4 Environment file

Create `/home/app/social_media_activities_app/.env` (owned by `app`, mode `600`). **Every
name below is a real env var read by the repo's settings** — see the grounding notes after.

```ini
# --- core ---
DJANGO_SETTINGS_MODULE=config.settings.prod
DJANGO_SECRET_KEY=<openssl rand -hex 48>                 # prod.py FAILS to boot on the dev default
DATABASE_URL=postgis://app:CHANGE_ME_STRONG@localhost:5432/app   # prod.py force-sets the PostGIS engine regardless
ASGI_THREADS=4
DB_POOL_ENABLED=True
DB_POOL_MIN_SIZE=0
DB_POOL_MAX_SIZE=4
DB_POOL_TIMEOUT=10

# --- hosts / CSRF (NON-RENDER BOX — set BOTH explicitly) ---
DJANGO_ALLOWED_HOSTS=app.example.eu                      # base.py: env.list("DJANGO_ALLOWED_HOSTS")
DJANGO_CSRF_TRUSTED_ORIGINS=https://app.example.eu       # prod.py env hook; MUST include the scheme

# --- Channels / Redis (cross-process WebSocket chat + global rate limits) ---
REDIS_URL=redis://localhost:6379/0
DJANGO_REQUIRE_SHARED_STATE=True                         # turn the prod.py soft-warning into a hard guard once >1 process

# --- object storage (private blobs: photos + adults-only PDFs) ---
MEDIA_STORAGE_BACKEND=apps.media.storage.S3StorageBackend
MEDIA_S3_BUCKET=socialapp-media
MEDIA_S3_ENDPOINT_URL=https://fsn1.your-objectstorage.com   # Hetzner Object Storage endpoint for your region
MEDIA_S3_REGION=eu-central                                  # any "eu*" value, OR rely on the endpoint_url (see guardrail)
MEDIA_S3_ADDRESSING_STYLE=virtual                          # Hetzner uses virtual-hosted-style; "path" for MinIO
AWS_ACCESS_KEY_ID=<hetzner S3 access key>
AWS_SECRET_ACCESS_KEY=<hetzner S3 secret>

# --- identity / age assurance (prod.py HARD-fails without a real anchor) ---
IDENTITY_PROVIDER=apps.accounts.identity.providers.eudi.EUDIWalletProvider
EUDI_SANDBOX=False
EUDI_TRUSTED_ISSUERS={"https://issuer.example.eu":"-----BEGIN PUBLIC KEY-----\n...\n-----END PUBLIC KEY-----"}

# --- retention (your DPO sets the period; 0 disables) ---
MESSAGING_RETENTION_DAYS=365                             # E2EE direct/group messages (messaging app)
# NOTE: thread Posts are permanent + audited — the former CHAT_RETENTION_DAYS / purge_chat job was
# removed (see config/settings/base.py), so do NOT set CHAT_RETENTION_DAYS; it is a dead variable.
```

**Grounding notes (why each value is what it is):**

- `DJANGO_SECRET_KEY` — `prod.py` raises `ImproperlyConfigured` if unset or left as the dev
  default `insecure-dev-key-change-me`. Generate a unique one.
- `DATABASE_URL=postgis://…` — `prod.py` **force-sets** `ENGINE = django.contrib.gis.db.backends.postgis`
  regardless of the URL scheme, so the GeoDjango backend is always used. It also adds a
  `statement_timeout` (default 30 s), disables per-thread ASGI persistence, and uses the bounded
  psycopg pool above. `ASGI_THREADS=4` keeps the matching Channels thread/connection ceiling small.
- `MEDIA_S3_*` — `apps/media/storage.py::S3StorageBackend` is a **custom boto3 client** (not
  django-storages). It reads `MEDIA_S3_BUCKET`, `MEDIA_S3_ENDPOINT_URL`, `MEDIA_S3_REGION`,
  `MEDIA_S3_ADDRESSING_STYLE` from settings and the AWS creds from boto3's default env chain.
  **Guardrail to satisfy:** `prod.py` asserts that an `S3StorageBackend` must have
  `MEDIA_S3_REGION` starting `eu` **OR** a non-empty `MEDIA_S3_ENDPOINT_URL` — otherwise it
  refuses to boot ("minors' data residency"). With Hetzner you set the endpoint *and* an `eu*`
  region, so you satisfy it twice over.
- `REDIS_URL` — when set, `base.py` switches **both** the cache (DRF throttles + the anti-abuse
  rate limiter) **and** the Channels layer to Redis. **Without `REDIS_URL` the channel layer is
  per-process `InMemoryChannelLayer`**, so `social.broadcast_post` only reaches WebSocket clients
  on the *same* process — cross-process chat silently fails to deliver. Set it from day one.
- `DJANGO_REQUIRE_SHARED_STATE=True` — `prod.py` warns (soft) about per-process backends on a
  single process, but **raises** if this flag is set while a per-process backend is still active.
  Set it so that the day you run a 2nd daphne worker, a misconfigured Redis fails *loudly* instead
  of silently dropping chat messages.
- Identity vars — `prod.py` hard-fails if the dev identity provider is used, if `EUDI_SANDBOX` is
  truthy, or if the EUDI provider has an empty `EUDI_TRUSTED_ISSUERS`. These are launch-blocking
  regardless of host. (Note `ALLOW_MINOR_ONBOARDING` stays `False` until a verifiable trust anchor
  + DPIA — minors are off by default in prod.)

### 3.5 Run daphne under systemd

`/etc/systemd/system/socialapp.service`:

```ini
[Unit]
Description=social activities app (daphne ASGI)
After=network.target postgresql.service redis-server.service
Requires=postgresql.service redis-server.service

[Service]
User=app
WorkingDirectory=/home/app/social_media_activities_app
EnvironmentFile=/home/app/social_media_activities_app/.env
# one-shot pre-start: apply migrations (PostGIS auto-enabled here) + collect static
ExecStartPre=/home/app/social_media_activities_app/.venv/bin/python manage.py migrate --noinput
ExecStartPre=/home/app/social_media_activities_app/.venv/bin/python manage.py collectstatic --noinput
ExecStart=/home/app/social_media_activities_app/.venv/bin/daphne -b 127.0.0.1 -p 8000 config.asgi:application
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now socialapp
```

> Static assets (admin, DRF UI) are served **in-process by WhiteNoise** in prod — `prod.py`
> inserts WhiteNoise middleware and `daphne` serves it, so the reverse proxy only needs to do
> TLS + proxy. No separate static web server or CDN required for launch.

### 3.6 TLS reverse proxy (Caddy)

`/etc/caddy/Caddyfile` — Caddy auto-provisions Let's Encrypt and proxies HTTP **and**
WebSockets (Channels chat) transparently:

```caddyfile
app.example.eu {
    encode zstd gzip
    reverse_proxy 127.0.0.1:8000        # WebSocket upgrades pass through automatically
}
```

```bash
sudo systemctl reload caddy
```

`prod.py` sets `SECURE_SSL_REDIRECT`, HSTS (1 yr), secure cookies, and trusts the
`X-Forwarded-Proto` header — all correct behind Caddy.

### 3.7 Periodic jobs (replace the paid Render cron)

`render.yaml` runs `python manage.py run_due_jobs` daily at **03:00 UTC** on a paid `cron`
service (retention purges, suspension lifts, reminders — see the `DUE_JOBS` fan-out in
`apps/ops/`). On a single box, use a systemd timer (no extra cost):

`/etc/systemd/system/socialapp-jobs.service`:
```ini
[Unit]
Description=run_due_jobs (retention/maintenance)
[Service]
Type=oneshot
User=app
WorkingDirectory=/home/app/social_media_activities_app
EnvironmentFile=/home/app/social_media_activities_app/.env
ExecStart=/home/app/social_media_activities_app/.venv/bin/python manage.py run_due_jobs
```
`/etc/systemd/system/socialapp-jobs.timer`:
```ini
[Unit]
Description=daily 03:00 UTC run_due_jobs
[Timer]
OnCalendar=*-*-* 03:00:00 UTC
Persistent=true
[Install]
WantedBy=timers.target
```
```bash
sudo systemctl enable --now socialapp-jobs.timer
```

> Equivalent plain crontab line if you prefer: `0 3 * * * cd /home/app/social_media_activities_app && set -a && . ./.env && .venv/bin/python manage.py run_due_jobs`.
> Set `MESSAGING_RETENTION_DAYS` (in `.env`, read by the job) to the period your DPO defines — `0`
> disables that purge. Running `run_due_jobs` is **required**: without it, retention never runs and
> suspensions never auto-lift (GDPR Art.5(1)(e) storage limitation + DSA proportionality). (Thread
> Posts are permanent + audited — there is no chat purge to configure.)

### 3.8 Agent API sidecar (optional)

A separate stdlib-only Go binary (`services/agentapi/`) can front high-volume AI-agent read
traffic instead of routing it through Django/daphne. It serves `GET /agent/v1/*` from the same
public-data JSON snapshot that `python manage.py export_agent_snapshot` writes (fanned out daily
by `run_due_jobs`, § 3.7) — it never opens a database connection itself, which matters because
`DB_POOL_MAX_SIZE=4` leaves very little Postgres connection headroom on this box for a second
consumer. Deploy it once agent traffic becomes material (crawl volume that would otherwise
compete with real users for daphne's threads/DB pool); skip it at launch — the sidecar is
optional and the Caddy route 502s harmlessly if it isn't running.

**Build** (no Go toolchain needed on the host — build in a throwaway container and copy the
static binary out):

```bash
docker run --rm -v "$PWD":/src -w /src/services/agentapi golang:1.23 \
  go build -o /src/services/agentapi/agentapi .
# or, using the service's own Dockerfile:
docker build -t agentapi-build services/agentapi
docker create --name agentapi-extract agentapi-build
docker cp agentapi-extract:/agentapi ./services/agentapi/agentapi
docker rm agentapi-extract
```

**Install on the box:**

```bash
sudo -u app mkdir -p /home/app/agentapi
scp services/agentapi/agentapi app@<ip>:/home/app/agentapi/agentapi
ssh app@<ip> chmod +x /home/app/agentapi/agentapi
sudo install -m 644 /home/app/social_media_activities_app/deploy/systemd/agentapi.service /etc/systemd/system/agentapi.service
sudo systemctl daemon-reload
sudo systemctl enable --now agentapi
```

**Routing:** `deploy/cloud-init.yaml.tftpl`'s Caddyfile already `handle`s `/agent/*` to
`127.0.0.1:8090` and falls through everything else to daphne on `127.0.0.1:8000` — no separate
Caddy config is needed once the systemd unit above is running.

**Env vars** (set in `deploy/systemd/agentapi.service`, not the Django `.env`):

| Var | Default | Meaning |
|---|---|---|
| `AGENT_API_ADDR` | `:8090` | Listen address; the unit binds `127.0.0.1:8090` (loopback only, Caddy fronts it). |
| `AGENT_SNAPSHOT_DIR` | `/data/agent_snapshot` | Where it reads the exported JSON snapshot; the unit points this at `/home/app/social_media_activities_app/var/agent_snapshot`. |
| `AGENT_API_TRUST_PROXY` | unset | Set to `1` behind Caddy so the sidecar's own rate limiter reads `X-Forwarded-For` correctly. |

**Django side:** `AGENT_SNAPSHOT_DIR` must also be set in the app's `.env` (it already is —
`deploy/cloud-init.yaml.tftpl` renders it) so `run_due_jobs` actually populates the snapshot
directory the sidecar reads. An empty/unset `AGENT_SNAPSHOT_DIR` in the Django `.env` disables
the export job (no snapshot is written), which leaves the sidecar serving nothing.

### 3.9 Optional: docker-compose variant

If you'd rather run the app in a container against host Postgres/Redis, a minimal
`docker-compose.prod.yml` (the systemd path above is simpler to operate — pick one):

```yaml
services:
  web:
    build: .                                  # uses the repo Dockerfile (daphne CMD)
    restart: unless-stopped
    network_mode: host                        # reach host's localhost Postgres + Redis
    env_file: .env                            # same vars as § 3.4
    command: >
      sh -c "python manage.py migrate --noinput &&
             daphne -b 127.0.0.1 -p 8000 config.asgi:application"
  jobs:
    build: .
    restart: "no"
    network_mode: host
    env_file: .env
    entrypoint: ["python", "manage.py", "run_due_jobs"]   # invoke from host cron, not always-on
```
(Run Postgres + Redis as host apt services as in § 3.1–3.2; Caddy as in § 3.6.)

### Non-Render hosts: ALLOWED_HOSTS + CSRF origins

On a non-Render box you set the public hostname/origin explicitly (the `RENDER_EXTERNAL_HOSTNAME`
auto-injection only happens on Render):

- **`DJANGO_ALLOWED_HOSTS=app.example.eu`** — `base.py` reads this (default `[]`).
- **`DJANGO_CSRF_TRUSTED_ORIGINS=https://app.example.eu`** — `prod.py` reads this (it merges with any
  Render-derived origin; **each value must include the scheme**, as Django requires). Without a trusted
  origin, HTTPS form POSTs from a custom domain fail CSRF.

```python
# config/settings/prod.py — both hooks
# ALLOWED_HOSTS  <- DJANGO_ALLOWED_HOSTS (base.py)
_CSRF_ORIGINS = env.list("DJANGO_CSRF_TRUSTED_ORIGINS", default=[])
if _CSRF_ORIGINS:
    CSRF_TRUSTED_ORIGINS = [*globals().get("CSRF_TRUSTED_ORIGINS", []), *_CSRF_ORIGINS]
```

> History: `prod.py` previously derived CSRF origins *only* from `RENDER_EXTERNAL_HOSTNAME`, with no
> hook for a custom domain — the `DJANGO_CSRF_TRUSTED_ORIGINS` env above closes that gap so a
> host-agnostic EU deploy is first-class.

---

## 4. Object storage detail

The app does **not** use django-storages. `apps/media/storage.py::S3StorageBackend` is a thin
**boto3** client (`s3v4` signing, configurable `addressing_style`) that talks to **any
S3-compatible endpoint** via `endpoint_url` — Hetzner Object Storage, Cloudflare R2, MinIO,
Backblaze B2 all work unchanged.

**How blobs are exposed:** never publicly. Objects are private; they are served only through a
**signed, expiring, per-viewer, membership-scoped URL** (`MEDIA_SIGNED_URL_TTL`, default
**300 s** — `base.py`), via the `AttachmentFileView` / signed-token path described in
`FEATURES_BUILT.md` ("Group-thread media"). PDFs are **adults-only** (`MEDIA_FILE_COHORTS`, default `["adult"]`) and
force-downloaded. So bucket configuration is simply: **private bucket, no public read** — the
app mints short-lived URLs; you never set an object ACL to public.

### Provider comparison

| Provider | Jurisdiction | Egress | Notes |
|---|---|---|---|
| **Hetzner Object Storage** *(recommended at launch)* | **EU-owned (DE/FI)** | ~€1/TB after 1 TB included | Simplest — same vendor/region as the box; no extra processor; EU residency is unambiguous. Use `MEDIA_S3_ADDRESSING_STYLE=virtual`. |
| Cloudflare R2 | US processor, **EU-jurisdiction flag** available | **$0 egress** | Cheapest if media bandwidth ever explodes; but US-owned → needs a **DPA + SCCs**, and you must set the EU-jurisdiction flag on the bucket. The `.env.example` already shows an R2 endpoint as an example. |
| Backblaze B2 (EU region) | US processor, EU region | metered | Cheap storage; US-owned → DPA/SCCs; less in-stack than Hetzner. |
| MinIO (self-host) | wherever you run it | n/a | **BANNED org-wide** (upstream repo archived 2026-02 — see `unified-deployment-architecture/docs/infrastructure-decision.md`); do not deploy. Also loses the "blobs survive a box rebuild" property. |

**Recommendation:** **Hetzner Object Storage at launch** (in-stack, EU-owned, residency trivially
satisfied). Switch to **Cloudflare R2** *only if* media egress ever grows enough that zero-egress
pricing dominates — at which point sign an R2 DPA + set the EU-jurisdiction flag, and the
`prod.py` guardrail is satisfied by R2's `endpoint_url` even without an `eu*` region string.

> **Org-rule caveat (2026-07-02):** the R2 fallback above can never apply to this app's user media.
> Org-wide, Cloudflare R2 serves ONLY the public, non-personal Gold corpus — anything
> borderline-personal, **including minors' media, never goes to R2**, DPA or not (see
> `unified-deployment-architecture/docs/infrastructure-decision.md`). If egress economics ever bite,
> the escape hatch is another EU-owned S3 provider, not R2.

### Backups: `pg_dump` → object storage

Use the **same bucket** (or a dedicated `…-backups` bucket) as the dump target, so backups stay
EU-resident with no extra vendor. Nightly, after `run_due_jobs` is fine:

```bash
#!/usr/bin/env bash
set -euo pipefail
TS=$(date -u +%F)
pg_dump --no-owner --format=custom --dbname=app \
  | gzip \
  | aws --endpoint-url "$MEDIA_S3_ENDPOINT_URL" s3 cp - "s3://socialapp-backups/pg/app-$TS.dump.gz"
# (aws-cli reads AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY from the env — same creds as the app)
```

Add a bucket **lifecycle rule** to expire backups after, e.g., 30 days (storage-limitation
hygiene), and test a restore (`pg_restore`) periodically. Keep backups in the **EU bucket only** —
do not copy them to a non-EU location.

---

## 5. Claude / AI posture

**Invariant #6: avoid per-user cloud-AI spend.** The product engine is intentionally
**deterministic / no-ML** — `social.thread_digest` (extractive, no ML), `social.draft_activity_text`
(template-only), and `communities` (predicate at read time) are all rule-based. There is **no LLM
in any user request path, and there must not be one.**

**Rule: NO Claude (or any LLM) in a per-user code path. Ever.** It would break invariant #6 (cost
scales with users), undercut the text-first / no-engagement-maxxing promise, and create a stream of
user content leaving the EU.

**Where Claude *may* appear (optional, admin-side, low-volume, offline only):**

- Examples: moderator **triage assist** (summarize a report queue for a human moderator), or
  one-off **seed-copy generation** (place blurbs, onboarding copy). All run **off the request
  path**, by staff, in bounded batches.
- Cost-bound it: prefer the **Batch API (50% off)** with **Claude Haiku** (cheapest model). A few
  hundred small admin calls/month is well under **€1/mo** and is **unrelated to user count** — it
  never scales with traffic. (See `docs`/the `claude-api` skill for current model ids + pricing.)

**EU data residency for any text sent to Claude:**

- The first-party **Anthropic API** has **no guaranteed EU-only region**. Posture as of **Sept 2025**:
  inputs/outputs retained **7 days** by default, **never used for training**, GDPR **DPA available**;
  Zero-Data-Retention (ZDR) is **Enterprise-tier only**.
- If EU residency is **mandatory** for any text you'd send (e.g. anything touching a minor — which
  you should simply **never** send), do **not** use the first-party API. Route via **AWS Bedrock**
  (Frankfurt `eu-central-1`, Ireland `eu-west-1`, Paris, Stockholm) or **Google Vertex AI** EU
  regions, where the inference stays in-region.

**Default, most invariant-aligned answer: ship with NO Claude dependency at all.** The platform
needs none, and adding one only adds cost, a US-transfer question, and a surface to get wrong.

---

## 6. Scaling path (when one box is outgrown)

Scale by *splitting state off the box*, not by rewriting. The app already supports this: set
`REDIS_URL` (shared cache + channel layer) and run multiple `daphne` workers — no code change.

1. **Split Postgres to a managed EU service.** **Scaleway Managed PostgreSQL** (France;
   **EU-owned**, Iliad group) explicitly ships **PostGIS 3.5** *and* **`pgvector` 0.8.1** — so it
   is a drop-in for this app's exact extension needs with **no app-code change** (keep
   `DATABASE_URL=postgis://…`; `prod.py` still forces the GeoDjango engine). Smallest production
   node ~**€28/mo**.
2. **Move Redis off-box.** **Upstash** (EU region) has a free tier (256 MB + 500k commands/mo) —
   point `REDIS_URL` at it. *Upstash is US-owned → sign a DPA if used.* Or use **Scaleway Managed
   Redis** to stay EU-owned and in-vendor with the DB.
3. **Run 2+ daphne workers** behind the reverse proxy. Now `REDIS_URL` is doing real cross-process
   work (chat fan-out + global rate limits), so keep `DJANGO_REQUIRE_SHARED_STATE=True` — it will
   hard-fail if Redis is ever misconfigured, instead of silently dropping WebSocket messages.

**Rough scaled total: ~€45–55/mo** (compute + managed Postgres + Redis + object storage) — still
well under a US PaaS, and still all-EU if you pick Scaleway for DB+Redis.

**Alternatives to keep on the radar:** **Render-Frankfurt** (lowest-ops, US processor — § 2);
**OVHcloud** (France, SecNumCloud-grade sovereignty) — but **verify PostGIS + `pgvector`** on
their managed Postgres before committing, same as you would for Render.

---

## 7. GDPR / DSA / EU-residency checklist

- [ ] **Compute region is EU** — Hetzner DE/FI (or Scaleway FR / OVH FR when scaled). Confirmed at
      provision time.
- [ ] **Object storage region is EU** — Hetzner DE/FI bucket; the `prod.py` guardrail
      (`MEDIA_S3_REGION` `eu*` **OR** `MEDIA_S3_ENDPOINT_URL` set) refuses to boot otherwise. ✔ by design.
- [ ] **Backups stay in EU** — `pg_dump` target is the EU object-storage bucket; lifecycle-expire;
      never copied out of region.
- [ ] **Signed DPA + SCCs with any US-owned processor** — applies if you use Cloudflare R2,
      Render, Upstash, Backblaze, AWS Bedrock, or the first-party Anthropic API. None are required by
      the launch stack (all-Hetzner) — only if you opt into one.
- [ ] **Minor data never leaves the EU** — no LLM in the request path; minors' media in the EU
      bucket; `ALLOW_MINOR_ONBOARDING=False` until a verifiable trust anchor + DPIA. Never send
      minor-related text to a non-EU AI endpoint.
- [ ] **App-level GDPR/DSA flows are already built** — GDPR **erasure** lives in the `ops` app;
      DSA Art.16/17 notice + moderation flows are in `safety`/`notifications` (see `FEATURES_BUILT.md`).
      Hosting must keep the daily `run_due_jobs` timer alive so retention/suspension-lift actually run.
- [ ] **TLS + secure transport** — Caddy/nginx Let's Encrypt; `prod.py` already enforces
      `SECURE_SSL_REDIRECT`, HSTS (1 yr), secure cookies, `X-Forwarded-Proto`.
- [ ] **Secrets fail-closed** — unique `DJANGO_SECRET_KEY`; real `IDENTITY_PROVIDER` +
      `EUDI_TRUSTED_ISSUERS`; `EUDI_SANDBOX=False` (all enforced by `prod.py` boot assertions).

---

## Cost tables

### Launch stack (single city, single box)

| Component | Provider | Est. €/mo | EU residency |
|---|---|---|---|
| Compute (CPX21; CX22 ≈ €4.49) | Hetzner Cloud (DE/FI) | 7.99 | ✅ EU-owned |
| PostgreSQL 16 + PostGIS + pgvector | co-located on the box | 0 | ✅ |
| Redis (cache + Channels) | co-located on the box | 0 | ✅ |
| `daphne` + WhiteNoise + Caddy TLS | co-located on the box | 0 | ✅ |
| Object storage (blobs + backups) | Hetzner Object Storage (DE/FI) | 4.99 | ✅ EU-owned |
| AI (optional, admin-only, Batch+Haiku) | none / Anthropic Batch | 0–<1 | n/a (avoid for minor data) |
| **Total** | | **≈ €13** (≈ €9.50 on CX22) | **all EU** |

### Scaled stack (when one box is outgrown)

| Component | Provider | Est. €/mo | EU residency |
|---|---|---|---|
| Compute (2× daphne workers; same/larger box) | Hetzner Cloud (DE/FI) | 8–16 | ✅ EU-owned |
| Managed PostgreSQL (PostGIS 3.5 + pgvector 0.8.1) | Scaleway (FR) | ~28 | ✅ EU-owned |
| Managed/free Redis | Upstash EU (free) or Scaleway Redis | 0–10 | ✅ (Upstash US-owned → DPA) |
| Object storage (blobs + backups) | Hetzner Object Storage | ~5 | ✅ EU-owned |
| **Total** | | **≈ €45–55** | **all EU** |

---

## References

- Hetzner Cloud (server types / pricing): https://www.hetzner.com/cloud
- Hetzner Object Storage (S3-compatible, EU, egress pricing): https://www.hetzner.com/storage/object-storage
- Scaleway Managed PostgreSQL: https://www.scaleway.com/en/managed-postgresql-mysql/
- Scaleway supported PostgreSQL extensions (PostGIS 3.5, pgvector 0.8.1): https://www.scaleway.com/en/docs/serverless-sql-databases/reference-content/supported-postgresql-extensions/
- Upstash Redis pricing (free tier): https://upstash.com/pricing/redis
- Cloudflare R2 pricing (zero egress): https://developers.cloudflare.com/r2/pricing/
- Fly.io pricing (Managed Postgres from $38): https://fly.io/docs/about/pricing/
- Railway pricing ($20/vCPU-mo + $10/GB-RAM-mo): https://railway.com/pricing
- Anthropic data retention (7-day default, never trained on; Sept 2025) + AWS Bedrock EU regions
  (eu-central-1 Frankfurt, eu-west-1 Ireland, Paris, Stockholm) — for the Claude posture in § 5.
