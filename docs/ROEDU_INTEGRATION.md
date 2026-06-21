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

Events:
- `apps/events/management/commands/sync_roedu_events.py` — pulls the `events`
  product, resolves each event to an existing `Place` (geo + name via
  `find_duplicate`), and upserts **facts only** (no scraped `description` — M2) via
  the shared `upsert_event`. Defaults to `--min-confidence 1.0` (JSON-LD/iCal only;
  NER events are held — M5).
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
  `fetch()` (`RawPlace` field mapping, RO country, coord coercion, skip-no-coords).

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

`source="roedu"` is deliberately NOT one of the OSM/Overture child-venue classes, so
a scraped venue stays child-venue-**UNKNOWN** (fail-closed) until a curated allowlist
promotes it — it is never routed through the OSM tag branch (design §11 M4).

## Remaining real gaps (not yet done)

1. **Attribution / license rendering (M3)** — there are still NO `attribution` /
   `license_name` fields on `Place` / `Event`, and no UI credit. Some upstream
   sources are CC-BY/SA and require visible attribution; add the fields + render.
2. **Curated child-venue allowlist for cultural venues (M4)** — the synthesized
   classes a roedu venue gets (`theatre` / `museum` / `gallery` / `cinema` /
   `arts_centre`) are **not** in `ChildVenueClass`
   (`apps/places/migrations/0007_seed_child_venue_classes.py` seeds
   library/park/sports_centre/school/community_centre/playground/nature_reserve/
   college only). Correct fail-closed behavior today, but there's no allowlist
   promoting safe cultural venues and no regression test asserting an unmapped
   `roedu` place is child-UNKNOWN.
3. **Scraped-event gating for minors (M5)** — `sync_roedu_events` holds
   `confidence < 1.0` events, but there is no separate staff-review queue for held
   NER events and no rule to suppress outbound `source_url` links for CHILD/TEEN.
4. **Nightly automation** — both commands are manual; no scheduled job / cron wiring
   pulls roedu places + events on a cadence yet.
5. **`amenity=cinema` → `open_air_cinema` is a stopgap** — there is no plain indoor
   "cinema" activity type in the taxonomy; the rule reuses `open_air_cinema` (which
   carries the "cinema" alias) at low confidence. A dedicated `cinema` activity slug
   would be cleaner.
