# ADR-0025: AI-agent & search-engine access surface (public read APIs + Go snapshot sidecar)

Date: 2026-07-12
Status: accepted

## Decision

Expose the platform's already-public open data (venues, events, taxonomy, and the adult
opt-in public activity cards) to AI agents and search engines through four coordinated
surfaces, keeping every safety gate in Python:

1. **Events JSON API is anonymous and agent-queryable** — `EventViewSet` is `AllowAny`,
   routed through the same `events_with_public_places()` gate the public web pages, feeds
   and sitemap already use (parity, not new exposure), with composable filters for agents:
   `place`, `activity`, `city`, `from`/`to` (400 on malformed dates — never silently widen
   a requested window), `q`, request-only `near_lon`/`near_lat`/`radius_m`, `include_past`.
   `PublicActivitiesView` (the sanctioned ADULT opt-in card surface) gains the same
   `from`/`to` bounds alongside its existing activity + proximity filters.
2. **Agent snapshot** — `export_agent_snapshot` (a `run_due_jobs` job, opt-in via
   `AGENT_SNAPSHOT_DIR`) writes gate-filtered JSON files (`events/places/activities/taxonomy/
   manifest`) via `apps/web/agent_snapshot.py`. Activities come ONLY from
   `public_activities()` (hard-coded ADULT + `is_publicly_listed`), places ONLY from
   `public_places()` with crowd-corrected `display_*` values, events ONLY from
   `upcoming_events()`. Files are written atomically; manifest last.
3. **Go sidecar `services/agentapi/`** — a stdlib-only static binary that serves the snapshot
   from memory at `/agent/v1/*` (filterable events/places/activities, taxonomy, manifest,
   OpenAPI 3.1, markdown landing) with ETag/304, `Cache-Control: public`, CORS `*`, per-IP
   token-bucket rate limiting, gzip, and no cookies. It never touches Postgres or Django.
   Deployment is optional (Caddy `handle /agent/*` + systemd unit are templated in `deploy/`).
4. **Search-engine dataset signals** — a public `/open-data/` page with schema.org `Dataset`
   JSON-LD (Google Dataset Search input) + bulk snapshot downloads served by Django
   (`/open-data/snapshot/<name>`, whitelist-only); `llms.txt` v2 documenting the
   machine-readable APIs; `robots.txt` `Allow` carve-outs for exactly
   `/api/v1/events`, `/api/v1/places`, `/api/schema/` so live-browsing agents are not blocked
   by the blanket `/api/` disallow.

## Context / why

Goal: AI agents and search engines should be able to consume and recommend the platform's
public activity/event/venue data, and the resulting traffic may be large relative to a
single cheap EU box (Postgres connection budget ≈4 for the app pool; no CDN).

- **Why a snapshot-serving sidecar instead of Go-reads-Postgres:** every child-safety and
  privacy invariant is enforced by Python service gates (`public_activities()`,
  `public_places()`, `upcoming_events()`). Re-implementing those gates in Go would create a
  second safety authority that can drift; connecting Go to Postgres would compete for the
  tiny connection budget. Serving a Django-exported, gate-filtered snapshot keeps one safety
  authority, needs zero DB connections, fails static (stale-but-safe data on export failure),
  and a ~5 MB scratch-image binary handles agent-scale read traffic within the "cheap,
  scalable, open-source" invariant.
- **Why not open `visible_activities()` or add Activity JSON-LD/sitemap entries:** cohort
  isolation. `social.Activity` stays out of every crawler surface (existing pinned tests);
  the ONLY anonymous activity exposure remains the ADULT + opt-in card subset already served
  by `PublicActivitiesView`. The snapshot exports exactly that card field subset (no owner,
  no description, no logistics fields, no web URL — activity pages are login-walled).
- **Why open `EventViewSet`:** events are cohort-blind venue data with no user linkage,
  already public via HTML/RSS/Atom/sitemap; keeping the JSON twin auth-walled only pushed
  agents to scrape HTML. DRF anon throttling (60/min) still applies; the sidecar is the
  intended high-volume path.
- **Why stdlib-only Go:** no supply-chain surface, trivial multi-stage Docker build (host has
  no Go toolchain), nothing to audit beyond the standard library.

## Consequences

- **Deferred: `event_ld` `offers`/`isAccessibleForFree` enrichment.** The Event source
  price/availability facts live in the in-flight `v_2` scraper/data-server lane, not on
  `main`; the enrichment (and the matching snapshot fields) land additively once that
  schema reaches `main` — the sidecar passes unknown record fields through verbatim, so
  no Go change will be needed.

- Agents get three tiers: canonical HTML+JSON-LD (best for citation), the DRF JSON API
  (60/min anon), and `/agent/v1/` (cached, rate-limited, high-volume). `llms.txt` explains
  which to use.
- The snapshot schema (documented in `apps/web/agent_snapshot.py` and mirrored by
  `services/agentapi` + its `testdata/`) is now a cross-language contract: additive changes
  flow through (the sidecar passes unknown fields verbatim); renames/removals require
  touching both sides and their tests.
- `deploy/` gains an optional unit; if the sidecar is not deployed, `/agent/*` 502s
  harmlessly and everything else is unaffected.
- Snapshot freshness is bounded by the `run_due_jobs` cadence; the manifest carries
  `generated_at` so consumers can see staleness. Export is opt-in (`AGENT_SNAPSHOT_DIR`
  unset → no-op job).
- If `Place` ever gains a `created_by` FK or Activity fields are added to the public card
  serializer, the snapshot exporter and its exact-key tests must be re-audited (they pin the
  exported key sets).
