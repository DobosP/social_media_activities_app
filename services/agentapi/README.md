# agentapi

A small, self-contained, **stdlib-only** Go HTTP service that serves this
platform's public open-data snapshot (events, places, activities,
taxonomy) to AI agents at high volume.

It is deliberately **database-free**: the native `export_agent_snapshot` job
(`services/server/internal/jobs/snapshot.go`, owned by the main app) writes a
gate-filtered, public, PII-free JSON snapshot to a directory on disk.
`agentapi` loads that directory into memory at startup and on a periodic
interval, and serves read-only queries against it. It never touches
Postgres, never writes anything but log lines to stdout, and has zero
third-party Go dependencies — the built binary is a single static
executable, safe to expose directly to the internet behind Caddy.

## Privacy invariants

- Never sets cookies.
- Never logs client IP addresses or any user/session identifier — request
  logs contain only fixed method/route classes, status and duration. Searches,
  record IDs, raw paths, peer IPs, headers and configuration values are redacted.
- The data it serves is already public and PII-free by construction
  upstream (the snapshot writer applies all product safety gates before
  writing a record to disk); this service additionally checks reviewed field
  allowlists, scalar shapes,
  unique canonical JSON keys and the hard-coded adult-only activity cohort.

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
| GET    | `/agent/v1/events`        | List events (filters: `activity`, `place`, `city`, `from`, `to`, `near` or `near_lat`/`near_lon`, `radius_m`, `q`, `limit`, `offset`). |
| GET    | `/agent/v1/events/{id}`   | Single event.                                              |
| GET    | `/agent/v1/places`        | List places (filters: `activity`, `city`, `near` or `near_lat`/`near_lon`, `radius_m`, `q`, `limit`, `offset`). |
| GET    | `/agent/v1/places/{id}`   | Single place.                                              |
| GET    | `/agent/v1/activities`    | List activity instances (filters: `activity`, `place`, `from`, `to`, `limit`, `offset`). |
| GET    | `/agent/v1/taxonomy`      | Taxonomy document, verbatim.                                |
| GET    | `/agent/v1/healthz`       | `200`/`503` snapshot availability + generation age. Not rate limited. |

List responses are wrapped in
`{"api_version","generated_at","count","total","limit","offset","license","site","data":[...]}`;
detail responses drop the paging fields. Errors are
`{"error":{"code","message"}}`. See `openapi.json` (or `GET
/agent/v1/openapi.json`) for the full schema, and `landing.md` for the
human-facing overview.

Every GET response carries CORS headers (`Access-Control-Allow-Origin: *`),
a `Cache-Control` header, and a SHA256 weak `ETag` (send `If-None-Match` for a
cheap `304`). Responses are gzip-compressed when the client sends
`Accept-Encoding: gzip` and the body is at least 1KB. Clients are rate
limited per-IP via a token bucket; `429` responses carry `Retry-After: 60`.

## Snapshot input contract

The migration decision and media/organization boundaries are in
[ADR-0031](../../docs/adr/0031-go-public-serving-foundation.md).
`apps/web/agent_snapshot.py` remains the trusted, safety-gated publisher.

- Manifest and all datasets use **schema 2**. Each manifest dataset names exactly
  `events.json`, `places.json`, `activities.json` or `taxonomy.json`, its count and
  the SHA256 digest of its exact UTF-8 bytes. Export schema-2 files before starting
  this binary; old schema-1 exports are rejected.
- Files are replaced atomically and the manifest is published last. The reader
  checks all digests, generation timestamps, schemas, counts and the unchanged
  manifest before atomically replacing the current snapshot. Rehashing the
  manifest detects changes even when mtime, generation and counts stay equal.
- Only reviewed fields are allowed, including nested venue/attribution and
  taxonomy objects. Duplicate/case-aliased keys, arbitrary objects in scalar
  fields, non-adult activities and duplicate IDs are rejected. New fields require
  a reviewed contract update in both publisher and reader.
- Files are flat, regular and confined with `os.OpenRoot`; symlinks and traversal
  names are rejected. Byte budgets: manifest 1 MiB, events 32 MiB, places 64 MiB,
  activities 8 MiB, taxonomy 8 MiB. Record caps match the exporter:
  10,000 / 50,000 / 2,000; taxonomy has a 50,000-entity cap. JSON nesting is ≤16.
- Datetimes use UTC RFC3339 with `Z`. Generation allows at most five minutes of
  positive clock skew. Health reports generation age separately from load age.

A rejected reload retains the previous validated snapshot. Missing startup data
returns `503 snapshot_unavailable`; static documentation remains available.
No maximum publication age is currently enforced. Export cadence, revocation,
withdrawal/erasure timing and a freshness budget need review before rollout.
This snapshot API cannot replace the live native Go `/api/v1/` routes: its historical
and GeoJSON contracts differ. Date-only filters here use UTC.

## Resource and ingress budgets

Startup accepts reload intervals 1–3600 seconds, rates 1–1,000,000/minute,
bursts 1–10,000 and configured list caps 1–1,000. The rate table holds at most
10,000 client keys and fails closed at capacity. It is local to one process;
multiple replicas require edge/shared throttling to maintain a global budget.

Requests have no body. URI path ≤512 bytes, raw query ≤2048 bytes, ≤16 unique
parameters, key ≤64 bytes and value ≤512 bytes; malformed, duplicate and control
characters are rejected. The HTTP server bounds headers, reads, writes and idle
connections. Enable `AGENT_API_TRUST_PROXY=1` only with proxy-only ingress and a
proxy that replaces forwarding headers; the rightmost valid IP is then used.

Compose's optional `agent` profile binds host loopback, mounts snapshots read-only
and runs without capabilities or privilege escalation, with 512 MiB/1 CPU/64 PID
budgets. These are testable ceilings, not a measured full-capacity sizing claim.
TLS and global abuse protection belong at the existing reverse proxy.

## Build & run

Build/test through the Go Docker image — Go version is pinned by `go.mod` and the Dockerfile:

```sh
docker run --rm -v "$PWD/services/agentapi:/src" -w /src \
  -e GOCACHE=/tmp/gocache -e GOFLAGS=-buildvcs=false golang:1.27.1-bookworm \
  sh -c 'test -z "$(gofmt -l .)" && go vet ./... && go test ./... -count=1'
```

Build the production image (multi-stage: digest-pinned `golang:1.27.1-bookworm` builder,
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

## Verification

`go vet ./...` and `go test -race ./...` cover the complete HTTP chain and unsafe
snapshot regressions. Build `go build -o agentapi-test .` before running
`apps/web/tests/test_agent_go_contract.py`: it exports real gated Django records
and compares the Go HTTP output to a Django-query oracle in an isolated PostGIS
stack. CI builds the executable so this contract test runs rather than skips.

`.github/workflows/go.yml` checks native/container behavior, Trivy and official
Go vulnerability databases. The current tagged source analyser predates Go 1.27
SSA syntax; package and compiled-binary scans run without that source call graph.
Both scan modes are enforcing. Dependabot tracks this module and its builder.
