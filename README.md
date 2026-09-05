# Social Activities App

A nonprofit, open-source platform for organizing **in-person** activities — sports
(basketball, table tennis, football), reading, board games, video games — by connecting
people to real physical places. Text-first and deliberately the opposite of image-perfect /
short-video social media.

This repository now implements the **full product engine (D1–D10 + four feature waves)**: the
foundation + Romanian place data, identity / age-cohorts + parental consent, the social core
with join-by-vote, safety/moderation, the unified activity thread with live delivery,
**end-to-end-encrypted direct & group messaging**, private media, richer place/event data,
booking, donations/ops, a server-rendered web UI, notifications and recommendations. It is
**not yet launched** — current state lives in **[STATUS.md](STATUS.md)**; the remaining
operational/legal gaps are in **[docs/PRODUCTION_READINESS.md](docs/PRODUCTION_READINESS.md)**.

## Stack

- **Django 5.2 LTS + Django REST Framework** (+ `djangorestframework-gis`, `django-filter`)
- **PostgreSQL + PostGIS** via **GeoDjango** — the single primary datastore (relational +
  geospatial + graph + `pgvector`; no separate graph/vector DB)
- **ASGI/Channels** for real-time thread delivery; S3-compatible object storage for blobs
- **React-compatible TypeScript 7 compiled with Preact 10/Vite 8** for interactive screens;
  Django keeps the document/SEO shell and the initial JS+CSS build is capped at 40 KiB gzip
- **OpenStreetMap / Overpass** as the first (free) place-data source, plus Overture, the
  RO-EDU data platform, and events feeds — see [docs/DATA_PROVIDERS.md](docs/DATA_PROVIDERS.md)
- **Deploy:** the launch target is a **single Hetzner EU box + Hetzner Object Storage** via
  `deploy/` (Terraform + cloud-init) — see [docs/HOSTING_EU.md](docs/HOSTING_EU.md) and
  `docs/adr/0001`. `render.yaml` is a **free-tier demo only**. The org-level hosting-provider
  procurement is intentionally **not yet finalized**; the IaC has never been applied.

## Quick start (Docker)

```bash
docker compose up --build
# web runs migrations, loads the local RO-EDU data seed once, then serves http://localhost:8000
docker compose exec web python manage.py createsuperuser   # for /admin
```

The image build also compiles the React-compatible Preact frontend (`frontend/`, Vite →
`static/frontend/`; see ADR-0016 and ADR-0022). For frontend work outside Docker: `cd frontend &&
npm ci && npm test && npm run build` (Node 24; the lock currently uses TypeScript 7.0.2, Preact
10.29.7, React Router 6.30.4, and Vite 8.1.4; `npm run dev` serves the SPA on :5173 proxying to
runserver).

### Local variant: host already runs Postgres on 5432 (dev machines)

Use the untracked `docker-compose.local.yml` (its db exposes no host port):

```bash
docker compose -f docker-compose.local.yml up -d          # NOTE: no --build (see below)
# pgvector once: exec -T db bash -lc "apt-get update && apt-get install -y postgresql-16-pgvector"
docker compose -f docker-compose.local.yml exec -T web pip install -r requirements-dev.txt
docker compose -f docker-compose.local.yml exec -T \
  -e DJANGO_SETTINGS_MODULE=config.settings.test -e DJANGO_SECRET_KEY=ci-secret-not-for-prod \
  -e DATABASE_URL=postgis://app:app@db:5432/app web pytest -q
```

The compose volume-mounts `./:/app`, so the running container always uses current code (no rebuild
needed). The production image installs `requirements.txt` only (no pytest) — install dev deps as
above. The CI matrix is `.github/workflows/ci.yml`; gate commands with expected output are in
`docs/agent-testing.md`; agent operating rules are in `AGENTS.md`.

## Quick start (local, no Docker)

Requires Postgres 16 + PostGIS and the GeoDjango native libs
(`gdal-bin libgdal-dev libgeos-dev libproj-dev binutils`).

```bash
python -m venv .venv && . .venv/bin/activate
pip install -r requirements.txt -r requirements-dev.txt   # prod installs requirements.txt only
cp .env.example .env          # then edit DATABASE_URL
python manage.py migrate
python manage.py runserver
```

## Ingesting places

Scoped to one administrative area (a city) at a time:

```bash
python manage.py ingest_places --source osm --city "Cluj-Napoca" --dry-run   # preview
python manage.py ingest_places --source osm --city "Cluj-Napoca"             # write
# Alternatives: --bbox minlon,minlat,maxlon,maxlat | --limit N | --min-confidence 0.5
```

Re-runs are **idempotent** (upsert keyed on `osm_type`+`osm_id`); user-confirmed/manual
activity links are never overwritten. The OSM-tag → activity mapping lives in
`apps/ingestion/mapping.py`.

## Web UI

A server-rendered web interface (`apps/web/`, session auth) sits on top of the API for end
users — open `http://localhost:8000/`:

- Sign up / log in, profile + avatar, declare interests, and **verify your age** via the EU
  Digital Identity wallet (OpenID4VP; a sandbox demo wallet stands in until the live one ships).
- Discover: interactive **places map** (Leaflet), a recommended-for-you feed, upcoming activities,
  and **"what's happening"** events (with place detail showing nearby events).
- Organise an activity; on its page: **join-by-vote**, text thread, private member photos, and
  **live chat** (WebSocket).
- Notifications, a guardian **wards** view, and a donation page. Moderation stays in `/admin/`.

## API

- `GET /api/taxonomy/categories/`, `GET /api/taxonomy/activities/` — the activity graph
- `GET /api/places/` — GeoJSON `FeatureCollection`. Filters:
  - `?activity=<slug>` `?city=` `?source=` `?min_confidence=` `?in_bbox=minx,miny,maxx,maxy`
  - `?near_lon=&near_lat=` orders nearest-first and adds `distance_m`; add `?radius_m=` to
    also filter within a radius (metres)
- `GET /api/docs/` — Swagger UI (`/api/schema/` for raw OpenAPI)
- `/admin/` — Django admin with an interactive map widget on places

## Project layout

```
config/             # settings (base/dev/prod/test), urls, wsgi/asgi
apps/               # 20 Django apps: accounts (identity/EUDI), taxonomy, places (PostGIS), ingestion
                    #   (source adapters + RO-EDU client, ingest_places/sync_roedu), events, social,
                    #   connections (tiered profiles), communities, chat + messaging (E2EE, consumers),
                    #   media, safety (moderation, Art.16/17, SSRF net), ops (DeferredTask/run_due_jobs),
                    #   notifications, recommendations, discovery, saved_searches, booking, donations, web (SSR UI)
frontend/           # Preact/Vite SPA → static/frontend/ (ADR-0016, ADR-0022)
services/agentapi/  # optional no-DB Go sidecar for AI-agent/crawler reads (:8090, compose profile `agent`; ADR-0025)
deploy/             # Terraform + cloud-init + systemd for the Hetzner EU box (never applied) — deploy/README.md
db/                 # seed data — db/README.md
tests/              # cross-app API schema/security/prod-hardening tests; per-app tests live in apps/<app>/tests/
docs/               # design, ops, compliance, ADRs — index at docs/README.md
```

## Tests & lint

```bash
pytest            # unit (mapping) + DB/API + ingestion (recorded Overpass fixture, no network)
ruff check . && ruff format --check .
pip-audit         # dependency vulnerability scan (release gate)
```

Dependencies are fully pinned (compiled from `requirements*.in`) and tracked for security — see
[`docs/SECURITY.md`](docs/SECURITY.md). Django is on the **5.2 LTS** line.

## Docs

**Full roadmap & design docs live in [`docs/`](docs/README.md)** — the phased plan (D1–D10) with a
dependency graph and feature traceability is in [`docs/ROADMAP.md`](docs/ROADMAP.md); see also
[ARCHITECTURE](docs/ARCHITECTURE.md), [COMPLIANCE](docs/COMPLIANCE.md), [SAFETY](docs/SAFETY.md),
[SECURITY](docs/SECURITY.md), and [DATA_AND_INTEGRATIONS](docs/DATA_AND_INTEGRATIONS.md).
Decisions are recorded in [`docs/adr/`](docs/adr/); dated audits/plans are archived in
[`docs/archive/`](docs/archive/).

Component READMEs: [deploy/README.md](deploy/README.md) · [db/README.md](db/README.md) ·
[services/agentapi/README.md](services/agentapi/README.md) (public landing text:
[services/agentapi/landing.md](services/agentapi/landing.md)). Dated history: [WORKLOG.md](WORKLOG.md).
