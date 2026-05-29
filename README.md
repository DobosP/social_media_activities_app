# Social Activities App

A nonprofit, open-source platform for organizing **in-person** activities — sports
(basketball, table tennis, football), reading, board games, video games — by connecting
people to real physical places. Text-first and deliberately the opposite of image-perfect /
short-video social media.

This repository now implements the **full product engine (D1–D10)**: the foundation +
Romanian place data, identity / age-cohorts + parental consent, the social core with
join-by-vote, safety/moderation, per-activity chat, **end-to-end-encrypted direct & group
messaging**, private media, richer place/event data, booking, donations/ops, a
server-rendered web UI, notifications and recommendations. It is **not yet
production-ready** — see the **[2026-05 audit](docs/AUDIT_2026-05.md)** for the verified
state, the remaining launch-blockers, and the child-safety hardening in progress.

## Stack

- **Django 5.1 + Django REST Framework** (+ `djangorestframework-gis`, `django-filter`)
- **PostgreSQL + PostGIS** via **GeoDjango** — the single primary datastore (relational +
  geospatial + graph; no separate graph DB)
- **OpenStreetMap / Overpass** as the first (free) place-data source; an Overture adapter
  seam is stubbed for later

## Quick start (Docker)

```bash
docker compose up --build
# web runs migrations (incl. the activity taxonomy seed) then serves http://localhost:8000
docker compose exec web python manage.py createsuperuser   # for /admin
```

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
config/          # settings (base/dev/prod/test), urls, wsgi/asgi
apps/taxonomy/   # ActivityCategory, ActivityType, ActivityRelation (+ seed migration)
apps/places/     # Place (PostGIS) + PlaceActivity edge + geo API
apps/ingestion/  # source adapters (overpass, overture-stub), mapping, ingest_places command
```

## Tests & lint

```bash
pytest            # unit (mapping) + DB/API + ingestion (recorded Overpass fixture, no network)
ruff check . && ruff format --check .
pip-audit         # dependency vulnerability scan (release gate)
```

Dependencies are fully pinned (compiled from `requirements*.in`) and tracked for security — see
[`docs/SECURITY.md`](docs/SECURITY.md). Django is on the **5.2 LTS** line.

## Roadmap

**Full roadmap & design docs live in [`docs/`](docs/README.md)** — the phased plan (D1–D9) with a
dependency graph and feature traceability is in [`docs/ROADMAP.md`](docs/ROADMAP.md); see also
[ARCHITECTURE](docs/ARCHITECTURE.md), [COMPLIANCE](docs/COMPLIANCE.md), [SAFETY](docs/SAFETY.md),
[SECURITY](docs/SECURITY.md), [DATA_AND_INTEGRATIONS](docs/DATA_AND_INTEGRATIONS.md), and
[MULTI_AGENT_BUILD](docs/MULTI_AGENT_BUILD.md) (parallel multi-agent development).

The items below were "later deliverables" when this README was written at D1; **they have
since been built** (D2–D10). The list is kept for historical context — see
[docs/ROADMAP.md](docs/ROADMAP.md) and [docs/AUDIT_2026-05.md](docs/AUDIT_2026-05.md) for
current, verified status:

- **Accounts & identity** — pluggable provider integrating the EU **EUDI Wallet** + the EU
  privacy-preserving **age-verification** app (age-band proof) plus parental consent;
  age-cohort matching so children interact only with similar-age peers.
- **Social core** — threads/posts tied to a `Place`; join-by-vote (configurable approval
  threshold of participants); private per-thread photos only.
- **Chat** — real-time over the ASGI seam; safety-by-design moderation (encryption/scanning
  kept swappable pending EU CSAR).
- **Data** — Overture adapter, optional paid Google Places enrichment, cross-source dedup,
  `opening_hours` parsing.
- **Booking** — per-provider adapters behind a common interface (deep-links first).
