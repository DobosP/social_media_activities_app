# RO-EDU integration — places & events from `romania_scraper`

This app consumes the **RO-EDU data platform** (`romania_scraper.dataapi`) as one
more ingestion source. Scope: **Cluj-Napoca first** (matches `INGEST_DEFAULT_CITY`),
national later. Full design: `../roedu/docs/ROEDU_INTEGRATION_DESIGN.md` (§4, §11).

The integration is merged on `main` (not a feature branch). This doc describes the
**current, working state** — what's wired, how to run a demo, and the real gaps left.

## What's wired (in `main` today)

Places:
- `apps/ingestion/sources/roedu_client.py` — vendored stdlib HTTP client for the
  data API (`urllib` only, no new dependency). Cursor pagination; **fail-closed** on
  the platform's license gate (a page with `available != true` yields nothing).
- `apps/ingestion/sources/ro_scraper.py` — `RomaniaScraperAdapter` (`name="roedu"`)
  reads the `venues` product → `RawPlace`. It synthesizes OSM-style `tags` from the
  venue name (`_tags_for`) so `ingestion.mapping` can attach a `PlaceActivity` edge.
- `Place` records now store optional source credit fields (`attribution`,
  `license_name`, `provenance_url`) and the RO-EDU adapter maps those fields when
  present. Blank upstream metadata stays blank.

Events:
- `apps/events/management/commands/sync_roedu_events.py` — pulls the `events`
  product, resolves each event to an existing `Place` (geo + name via
  `find_duplicate`), and upserts **facts only** (no scraped `description` — M2) via
  the shared `upsert_event`. Defaults to `--min-confidence 1.0` (JSON-LD/iCal only;
  NER events are held — M5).
- `Event` records now store the same optional source credit fields and
  `sync_roedu_events` maps RO-EDU/source metadata into them when available.
- `Event.Source.SCRAPER = "roedu"` exists (`apps/events/models.py`) and the
  `0006_alter_event_source` migration is committed.
- `BatchEventsView` (`apps/ingestion/views.py`) allows `source="roedu"` implicitly
  (it allowlists `set(Event.Source.values)`), so an external pusher can POST roedu
  events to `/api/ingestion/batch-events/` as well.

Mapping:
- `apps/ingestion/mapping.py` has `TagRule`s for every tag `_tags_for` can emit:
  `amenity=theatre` → `theatre_show`, `tourism=museum` → `museum_visit`,
  `tourism=gallery` → `museum_visit`, `amenity=cinema` → `open_air_cinema`,
  `amenity=library` → `reading`, and the `arts_centre` default → a low-confidence
  generic-venue fan-out (`workshop`/`dance_social`/`board_games`).

Tests (no network, no DB — `django.test.SimpleTestCase`):
- `apps/ingestion/tests/test_roedu_client.py` — config/env defaults, header +
  URL/param building (drops `None`), cursor following, `available=false` fail-closed,
  `max_records`.
- `apps/ingestion/tests/test_roedu_adapter.py` — the full `_tags_for` heuristic and
  `fetch()` (`RawPlace` field mapping, RO country, coord coercion, skip-no-coords,
  optional attribution/license/provenance mapping).
- DB-backed regression tests cover RO-EDU event credit mapping, API credit rendering,
  web credit rendering, and `source="roedu"` child-venue fail-closed behavior.

## Running a demo (order matters)

The adapter is **pluggable, not built in**: `ingest_places` only knows about a
`roedu` source if you register the adapter via `settings.INGESTION_EXTRA_ADAPTERS`.
This is the #1 footgun — without it you get `CommandError: Unknown source: roedu`.

1. Set env (see `.env.example`):
   ```bash
   ROEDU_API_URL=http://<scraper-host>:8077
   ROEDU_API_KEY=social-app-dev
   INGESTION_EXTRA_ADAPTERS={"roedu": "apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter"}
   ```
   (`INGESTION_EXTRA_ADAPTERS` is read as JSON: `config/settings/base.py` does
   `env.json("INGESTION_EXTRA_ADAPTERS", default={})`.)

2. **Ingest venues first** — events need their Places to already exist so they can be
   matched:
   ```bash
   python manage.py ingest_places --source=roedu --city="Cluj-Napoca"
   ```

3. **Then ingest events** (resolves each event to a venue Place; facts only):
   ```bash
   python manage.py sync_roedu_events --city="Cluj-Napoca"
   # --dry-run to preview; --min-confidence 0 to include held NER events.
   ```

`sync_roedu_events` does NOT use `INGESTION_EXTRA_ADAPTERS` (it instantiates
`RoeduClient` directly), but it depends on step 2 having run, so keep the env set
for the whole sequence.

## Safety model

The platform enforces the license/GDPR gate **server-side** (the `social-app-dev`
key is redistributable-only, no `tdm_exception`). Treat that as defence-in-depth —
the client also fails closed (`available != true` ⇒ no records).

`source="roedu"` is deliberately NOT routed through the OSM/Overture child-venue
class branches, even though its venue adapter synthesizes OSM-style cultural tags
for activity mapping. A scraped venue stays child-venue-**UNKNOWN** (fail-closed)
until staff add a per-place `ApprovedChildVenue` approval. Regression tests assert
that an unmapped `roedu` theatre/museum is not child-safe, and that the explicit
staff-approval path still promotes a specific venue.

Public/member render paths now show a neutral "Source credit" line for places and
events when attribution/license/provenance metadata exists. The serializers expose
the same fields plus an `attribution_credit` display object.

## Remaining real gaps (not yet done)

1. **Curated cultural child-venue policy (M4 follow-up)** — this slice kept the
   product fail-closed and added regression coverage. It did **not** add a broad
   RO-EDU cultural class allowlist. Staff can approve vetted individual venues via
   `ApprovedChildVenue`; a broader theatre/museum/gallery/cinema policy still needs
   safeguarding review before seeding any class-level rule.
2. **Scraped-event gating for minors (M5)** — `sync_roedu_events` holds
   `confidence < 1.0` events, but there is no separate staff-review queue for held
   NER events and no rule to suppress outbound `source_url` links for CHILD/TEEN.
3. **Nightly automation** — both commands are manual; no scheduled job / cron wiring
   pulls roedu places + events on a cadence yet.
4. **`amenity=cinema` → `open_air_cinema` is a stopgap** — there is no plain indoor
   "cinema" activity type in the taxonomy; the rule reuses `open_air_cinema` (which
   carries the "cinema" alias) at low confidence. A dedicated `cinema` activity slug
   would be cleaner.
