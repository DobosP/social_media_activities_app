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

For native build, configuration, migration/adoption and operator commands see
[the Go server guide](docs/NATIVE_SERVER.md). Python sources and tests remain offline
contract oracles; the default Docker image contains no Python runtime.

## Stack

- **Go 1.27.1** native serving backend; selection and limits: [ADR-0032](docs/adr/0032-complete-native-go-backend.md)
- **PostgreSQL + PostGIS** via **pgx** — the single primary datastore (relational +
  geospatial + graph + `pgvector`; no separate graph/vector DB)
- **Go WebSockets + PostgreSQL notifications** for live delivery; private EU-native S3 storage for blobs
- **React-compatible TypeScript 7 compiled with Preact 10/Vite 8** for interactive screens;
  Native Go renders the document/SEO shell and the initial JS+CSS build is capped at 40 KiB gzip
- **OpenStreetMap / Overpass** as the first (free) place-data source, plus Overture, the
  RO-EDU data platform, and events feeds — see [docs/DATA_PROVIDERS.md](docs/DATA_PROVIDERS.md)
- **Deploy:** the launch target is a **single Hetzner EU box + Hetzner Object Storage** via
  `deploy/` (Terraform + cloud-init) — see [docs/HOSTING_EU.md](docs/HOSTING_EU.md) and
  `docs/adr/0001`. `render.yaml` is a **free-tier demo only**. The org-level hosting-provider
  procurement is intentionally **not yet finalized**; the IaC has never been applied.

## Quick start (native Docker)

```bash
docker compose up --build
# Go schema bootstrap runs once; web serves http://127.0.0.1:8000.
# An explicit development seed is optional, with no network ingestion:
docker compose --profile demo run --rm seed
```

Create an initial administrator through the native operator command, sending the
username/password JSON through private stdin (never command arguments or logs):
`docker compose run --rm -T web social-server --job createsuperuser --job-options -`.
Bootstrap creates a fresh unverified/unassigned identity; age and parental assurance
still use their governed verification flows. See [native configuration](services/server/cmd/social-server/README.md).

The image compiles the Preact frontend into hashed `static/frontend` assets with
Node 24. Local frontend checks: `npm --prefix frontend ci`, `npm --prefix frontend test`,
`npm --prefix frontend run build`. The Go server guide covers native host requirements
and explicit PostgreSQL/codec regression. Older untracked Django Compose variants
are reference environments; build them with `Dockerfile.reference` when needed.

## Ingesting places

Native operator commands accept one bounded JSON options object through `--job-options`.
For an owner-authorized preview, invoke `social-server --job ingest_places --job-options -`
with `source`, `city`, `dry_run` and bounded `limit`/`bbox` options. Writes are explicit;
startup runs no ingestion. Source mapping and provenance are preserved by the native
[command registry](services/server/internal/commands/README.md).

## Web UI

A server-rendered web interface (`apps/web/`, session auth) sits on top of the API for end
users — open `http://localhost:8000/`:

- Sign up / log in, profile + avatar, declare interests, and **verify your age** via the EU
  Digital Identity wallet (OpenID4VP; signed issuer/holder proofs and configured trust anchors are required).
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
- `/admin/` — guarded native operator pages with audited curated edits and governed domain actions

## Project layout

```
services/server/    # native HTTP/auth/domain/safety/media/HTML/live/job assembly (Go)
services/authcore/  # reviewed portable shared identity module + SOURCE.json hashes
services/agentapi/  # optional independent no-DB Go public snapshot reader (:8090)
frontend/           # Preact/Vite TypeScript client → static/frontend/
templates/, locale/ # native Go document/SEO rendering uses the existing presentation assets
apps/, config/      # offline Django contract/migration oracle; apps/web/templates are shared data
deploy/             # native artifact/systemd/cloud-init templates; Terraform never applied
db/                 # explicit local/demo seed data (db/README.md)
tests/              # offline Python reference tests; native tests live under services/server
docs/              # native guides, safety contracts, ADRs and verification receipts
```

## Tests & lint

```bash
go -C services/server test -race ./...
go -C services/server vet ./...
go -C services/server run ./cmd/check-authcore
```

Database-required cases need the explicit disposable fixture gate in
[agent-testing](docs/agent-testing.md); skipped database cases do not qualify a release.
Frontend and source/package/image audits are enforcing native CI gates. Python `pytest`,
Ruff, migration drift and pip audits remain the offline compatibility-oracle checks.
Dependency boundaries and security commands: [SECURITY](docs/SECURITY.md).

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
