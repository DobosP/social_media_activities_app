# agentapi

A small, self-contained, **stdlib-only** Go HTTP service that serves this
platform's public open-data snapshot (events, places, activities,
taxonomy) to AI agents at high volume.

It is deliberately **database-free**: a Django management command
(`apps/web/agent_snapshot.py`, owned by the main app) writes a
gate-filtered, public, PII-free JSON snapshot to a directory on disk.
`agentapi` loads that directory into memory at startup and on a periodic
interval, and serves read-only queries against it. It never touches
Postgres, never writes anything but log lines to stdout, and has zero
third-party Go dependencies — the built binary is a single static
executable, safe to expose directly to the internet behind Caddy.

## Privacy invariants

- Never sets cookies.
- Never logs client IP addresses or any user/session identifier — request
  logs contain only method, path, status, duration, and a truncated query
  string.
- The data it serves is already public and PII-free by construction
  upstream (the snapshot writer applies all product safety gates before
  writing a record to disk); this service adds no additional filtering
  logic of its own around *what* is public, only *how* it's queried.

## Configuration

All configuration is via environment variables; every one has a default.

| Variable                    | Default                 | Meaning                                                                 |
| ---------------------------- | ------------------------ | ------------------------------------------------------------------------ |
| `AGENT_API_ADDR`             | `:8090`                  | Listen address.                                                         |
| `AGENT_SNAPSHOT_DIR`         | `/data/agent_snapshot`   | Directory containing `manifest.json` + dataset files.                  |
| `AGENT_API_RELOAD_SECONDS`   | `30`                     | How often to check the snapshot directory for changes.                 |
| `AGENT_API_RATE_PER_MIN`     | `300`                    | Token bucket refill rate, per client, per minute.                      |
| `AGENT_API_RATE_BURST`       | `60`                     | Token bucket burst capacity, per client.                               |
| `AGENT_API_TRUST_PROXY`      | *(unset)*                | Set to `1` when behind a proxy that sets `X-Forwarded-For` (Caddy). Client identity for rate limiting becomes the last hop of that header; otherwise the raw TCP peer address is used. Never logged either way. |
| `AGENT_API_MAX_LIMIT`        | `200`                    | Hard cap on the `limit` query parameter for list endpoints.            |

## Endpoints

All endpoints are rooted at `/agent/v1` (this service serves the full
path; a reverse proxy in front of it should route `/agent/*` to it
without stripping the prefix).

| Method | Path                      | Description                                             |
| ------ | ------------------------- | --------------------------------------------------------- |
| GET    | `/agent/v1/`              | Markdown landing document.                                |
| GET    | `/agent/v1/openapi.json`  | OpenAPI 3.1 description of this API.                       |
| GET    | `/agent/v1/manifest`      | Snapshot manifest verbatim + server load info.              |
| GET    | `/agent/v1/events`        | List events (filters: `activity`, `city`, `from`, `to`, `near`, `radius_m`, `q`, `limit`, `offset`). |
| GET    | `/agent/v1/events/{id}`   | Single event.                                              |
| GET    | `/agent/v1/places`        | List places (filters: `activity`, `city`, `near`, `radius_m`, `q`, `limit`, `offset`). |
| GET    | `/agent/v1/places/{id}`   | Single place.                                              |
| GET    | `/agent/v1/activities`    | List activity instances (filters: `activity`, `place`, `from`, `to`, `limit`, `offset`). |
| GET    | `/agent/v1/taxonomy`      | Taxonomy document, verbatim.                                |
| GET    | `/agent/v1/healthz`       | `200`/`503` liveness + snapshot freshness. Not rate limited. |

List responses are wrapped in
`{"api_version","generated_at","count","total","limit","offset","license","site","data":[...]}`;
detail responses drop the paging fields. Errors are
`{"error":{"code","message"}}`. See `openapi.json` (or `GET
/agent/v1/openapi.json`) for the full schema, and `landing.md` for the
human-facing overview.

Every GET response carries CORS headers (`Access-Control-Allow-Origin: *`),
a `Cache-Control` header, and a strong `ETag` (send `If-None-Match` for a
cheap `304`). Responses are gzip-compressed when the client sends
`Accept-Encoding: gzip` and the body is at least 1KB. Clients are rate
limited per-IP via a token bucket; `429` responses carry `Retry-After: 60`.

## Snapshot input contract

Pointer: `apps/web/agent_snapshot.py` (owned by the Django app) is the
writer side of this contract. Summary:

- `manifest.json` is always written **last** by the writer, so this
  service can safely key reloads off manifest mtime/size changes without
  ever observing a half-written dataset.
- `events.json` / `places.json` / `activities.json` share a
  `{"schema_version","generated_at","count","records":[...]}` envelope.
  Individual records may carry fields beyond what this service reads;
  unknown fields are passed through to API responses untouched.
- `taxonomy.json` is served verbatim.
- All datetimes are UTC RFC3339 with a `Z` suffix.

On any read/parse error during a reload attempt, this service logs the
error and keeps serving the previously loaded snapshot (fail-static). A
missing snapshot directory/files at startup is not fatal — data endpoints
return `503 snapshot_unavailable` until the first successful load; the
landing doc, OpenAPI doc, and `/healthz` always work.

## Build & run

Build/test through the Go Docker image — the host has no Go toolchain:

```sh
docker run --rm -v "$PWD/services/agentapi:/src" -w /src \
  -e GOCACHE=/tmp/gocache -e GOFLAGS=-buildvcs=false golang:1.23 \
  sh -c 'test -z "$(gofmt -l .)" && go vet ./... && go test ./... -count=1'
```

Build the production image (multi-stage: `golang:1.23-alpine` builder,
`scratch` final image, non-root `USER 65534`, no shell):

```sh
docker build -t agentapi services/agentapi/
```

Run it:

```sh
docker run --rm -p 8090:8090 \
  -e AGENT_SNAPSHOT_DIR=/data/agent_snapshot \
  -v /path/to/snapshot:/data/agent_snapshot:ro \
  agentapi
```

## Deployment

This service is meant to sit behind the site's reverse proxy (Caddy in
production) at `/agent/*`, proxied with `handle` (no path stripping,
since this service already serves the full `/agent/v1/...` paths).
Compose/deploy wiring is owned by another part of the platform (see
`deploy/`), not by this package.
