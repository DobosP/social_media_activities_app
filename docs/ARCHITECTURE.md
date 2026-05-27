# Architecture

How the system is shaped today (D1) and how every future deliverable plugs into seams that
already exist. See [ROADMAP](ROADMAP.md) for sequencing.

## Principles

- **Postgres is the primary datastore.** Relational data, the activity **graph**, and
  **geospatial** data all live in PostgreSQL + PostGIS. No separate graph database. Blobs
  (the few images) are the only thing that leave Postgres — they go to S3-compatible object
  storage (D6).
- **Modular monolith.** One Django project, many focused apps under `apps/`. Cheap to run and
  deploy; split out services only if/when a real bottleneck appears.
- **Source-agnostic ingestion.** Place data arrives through `SourceAdapter`s, normalized to a
  `RawPlace`, so adding Overture/Google later doesn't touch the command logic.
- **Provenance & confidence are first-class.** Every place and every place↔activity edge records
  where it came from and how confident we are — this is what lets open-data inference, user
  contributions, and moderation coexist.
- **Safety & privacy by design.** Minimize identity data (age *bands*, not birthdates), isolate
  cohorts, default to private.

## Current shape (D1)

```
config/                 project settings (base/dev/prod/test), urls, wsgi, asgi
apps/
  taxonomy/             ActivityCategory, ActivityType (is-a tree), ActivityRelation (typed edges)
  places/               Place (PostGIS geography point) + PlaceActivity edge; GeoJSON API; admin map
  ingestion/            SourceAdapter seam (Overpass built, Overture stub), OSM->activity mapping,
                        ingest_places management command
```

### The knowledge graph, in plain Postgres

- **Hierarchy (is-a):** `ActivityCategory.parent` and `ActivityType.parent` self-references give a
  cheap tree (e.g. *basketball* → *team sport* → *sport*).
- **Lateral links:** `ActivityRelation` is a typed edge (`related` / `synonym` / `variant` /
  `requires`) for what a tree can't express (e.g. *table tennis* ↔ *ping pong*).
- **Activity ↔ place:** `PlaceActivity` is the edge connecting a `Place` to the `ActivityType`s it
  supports, carrying `origin` (inferred/confirmed/manual), `confidence`, and `mapping_rule`.

This is the structure the whole product hangs off: discovery, recommendations (D7), and activity
creation (D3) all traverse it.

### Geospatial

`Place.location` is a PostGIS **geography** point (SRID 4326), so distance queries return true
**metres**. A GiST index is auto-created. Proximity is exposed via the places API
(`?near_lon=&near_lat=`, optional `?radius_m=`, plus `?in_bbox=`).

## Seams for future deliverables

These already exist so later work is additive, not invasive:

| Future need | Seam it plugs into |
|---|---|
| Users / identity / age (D2) | `accounts.User` becomes `AUTH_USER_MODEL` (**do first**); `Place.created_by`, `PlaceActivity.confirmed_by` `# FUTURE:` FKs |
| Threads, activities, join-by-vote (D3) | New `apps/social/`; references `Place` + `ActivityType`; uses D2 cohorts |
| Safety & moderation (D4) | `PlaceActivity.origin` (protect confirmed/manual), audit logging, Django-admin queues |
| Chat (D5) | `config/asgi.py` (swap `get_asgi_application()` for a `ProtocolTypeRouter`) |
| Media (D6) | Object-storage backend; image fields on profile + thread posts |
| More place data (D7) | New `SourceAdapter` (Overture stub already present); cross-source dedup keyed off `source` + `raw_tags` |
| Booking (D8) | New `BookingProvider` adapter interface, analogous to `SourceAdapter` |

### Identity provider abstraction (D2)

Define an `IdentityProvider` interface returning an **assurance result** (verified age band +
parental-consent status), not raw identity. Concrete implementations: EUDI Wallet / EU
age-verification app. The rest of the app depends only on the interface — see
[COMPLIANCE](COMPLIANCE.md).

## Data flow: ingestion (today)

```
Overpass API ──> OverpassAdapter.fetch() ──> RawPlace ──> ingest_places
                                                              │
                          match_element(tags)  ◄─────────────┘  (mapping.py)
                                                              │
                 update_or_create(Place)  +  PlaceActivity edges (idempotent)
```

Idempotency comes from partial-unique constraints (`uq_place_osm`) + `update_or_create`;
user-confirmed/manual edges are never overwritten by re-ingestion.

## Deployment (target)

- **Now → small:** single EU VPS (e.g. Hetzner) running Docker Compose (PostGIS + app). EU data
  residency from day one (GDPR + children's data).
- **Growing:** managed EU Postgres; app horizontally scalable (stateless web + ASGI workers for
  chat); object storage for blobs; CDN for static; caching as needed.
- **Cost discipline:** free open-data sources first; paid APIs (Google) only where they earn
  their keep; donation-funded, so the footprint stays lean.

## Tech choices & rationale (quick reference)

- **Django + DRF** — batteries-included (admin = moderation tooling), mature GeoDjango, fast for a
  small/nonprofit team.
- **PostGIS / GeoDjango** — geo in the same DB as everything else.
- **Relational graph (no graph DB)** — simpler ops, one datastore, fine at this scale; `pgvector`
  later for similarity (D7).
- **Adapter patterns** (sources, identity, booking) — isolate third parties behind interfaces.
