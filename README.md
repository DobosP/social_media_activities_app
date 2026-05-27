# Social Activities App

A nonprofit, open-source platform for organizing **in-person** activities — sports
(basketball, table tennis, football), reading, board games, video games — by connecting
people to real physical places. Text-first and deliberately the opposite of image-perfect /
short-video social media.

This repository currently contains **Deliverable 1**: the foundation and the Romanian
place-data pipeline (the activity↔place "knowledge graph"). User accounts, identity /
age-verification, threads, join-by-vote and chat are intentionally **not** built yet — see
[Roadmap](#roadmap).

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
pip install -r requirements.txt
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
```

## Roadmap

Captured for later deliverables (not built yet):

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
