# agentapi — open-data snapshot API for AI agents

This is a small, read-only HTTP API that serves this platform's public
open-data snapshot (events, places, activities, taxonomy) to AI agents at
high volume. It is deliberately database-free: a periodic job on the main
site writes a gate-filtered, public, PII-free JSON snapshot to disk, and
this service loads it into memory and answers queries against it.

The human-facing version of this same data lives at `{{SITE}}/open-data/`.
This API is meant for programmatic/agent consumption of the same dataset.

## Endpoints

All endpoints are rooted at `/agent/v1`.

| Method | Path                     | Description                                   |
| ------ | ------------------------ | ---------------------------------------------- |
| GET    | `/agent/v1/`             | This document (`text/markdown`).               |
| GET    | `/agent/v1/openapi.json` | Machine-readable OpenAPI 3.1 description.       |
| GET    | `/agent/v1/manifest`     | Snapshot manifest + server load info.           |
| GET    | `/agent/v1/events`       | List events. Filters below.                     |
| GET    | `/agent/v1/events/{id}`  | Single event by id.                             |
| GET    | `/agent/v1/places`       | List places. Filters below.                     |
| GET    | `/agent/v1/places/{id}`  | Single place by id.                             |
| GET    | `/agent/v1/activities`   | List activity instances. Filters below.         |
| GET    | `/agent/v1/taxonomy`     | The activity/category taxonomy, verbatim.       |
| GET    | `/agent/v1/healthz`      | Liveness + snapshot freshness (not rate limited).|

## Query parameters

- `events`: `activity` (slug equality), `city` (case-insensitive equality),
  `from` / `to` (RFC3339 or `YYYY-MM-DD`; `to` is an exclusive upper bound —
  a date-only `to` covers the whole day), `near=lat,lon` + `radius_m`
  (haversine, default 5000m, max 100000m), `q` (case-insensitive substring
  on title), `limit` (default 50, max see `/manifest` / this deployment's
  configured cap), `offset`.
- `places`: `activity` (membership in the place's activity types), `city`,
  `near` / `radius_m`, `q` (substring on name), `limit`, `offset`.
- `activities`: `activity` (activity_type equality), `place` (place id),
  `from` / `to`, `limit`, `offset`.

Every list response is an envelope:
`{"api_version","generated_at","count","total","limit","offset","license","site","data":[...]}`.
Detail responses drop the paging fields. Errors are
`{"error":{"code","message"}}` with an appropriate HTTP status.

## Rate limits

Requests are rate-limited per client using a token bucket (refill rate and
burst are set per deployment; see response headers and `429` bodies, which
include `Retry-After: 60`). `/agent/v1/healthz` is exempt.

## Caching

List/detail/manifest/taxonomy responses carry `Cache-Control: public,
max-age=300` and a strong `ETag`; send `If-None-Match` to get `304 Not
Modified` cheaply. This document and the OpenAPI description use
`max-age=3600`.

## License & attribution

Every response includes a `license` field sourced from the snapshot
manifest — check it and preserve the attribution it names when you reuse
this data. This snapshot excludes anything not cleared for public,
PII-free redistribution.
