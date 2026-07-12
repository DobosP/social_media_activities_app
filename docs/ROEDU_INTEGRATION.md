# RO-EDU integration — places & events from `romania_scraper`

This app consumes the **RO-EDU data platform** (`romania_scraper.dataapi`) as one
more ingestion source. Scope: **Cluj-Napoca first** (matches `INGEST_DEFAULT_CITY`),
national later. Full design: `../roedu/docs/ROEDU_INTEGRATION_DESIGN.md` (§4, §11).

The original product integration is on `main`; the canonical promoted-snapshot contract described
below is implemented locally on `v_2` and remains intentionally unmerged while the wider RO-EDU
Phase 0 gate is open. This doc describes that current branch state.

## What's wired on `v_2`

Places:
- `apps/ingestion/sources/roedu_client.py` — vendored stdlib HTTP client for the
  data API (`urllib` only, no new dependency). Cursor pagination; **fail-closed** on
  the platform's license gate (a page with `available != true` yields nothing).
  Its promoted app-pack reader accepts exactly
  `roedu:social_media_activities_app:events_places:v1` at the redistributable
  `/v1/app-packs/social_media_activities_app/<pack>` endpoint. Short aliases and other app names
  are rejected before network or database work. Every page must retain one schema-v1
  pack/release/snapshot identity, an offset-aware generation time, coherent full/partial
  completeness, a bounded cursor, and a valid result envelope. Duplicate or malformed items,
  producer withholding/errors, a record limit, or relationship drift makes the read incomplete,
  so it cannot infer absence.
- The client validates exact venue/event/tombstone output shapes. Public fields are bound to policy
  schema 4/ruleset 6 and acquisition schema 3 through a path-free attestation. Full policy,
  clearance, rights, acquisition evidence, source prose/person fields, internal paths/checksums,
  unsafe URLs, and unknown fields fail closed. The generic product iterator remains only for the
  explicitly configured legacy delivery mode.
- `apps/ingestion/sources/ro_scraper.py` — `RomaniaScraperAdapter` (`name="roedu"`)
  reads the `venues` product → `RawPlace`. It synthesizes OSM-style `tags` from the
  venue name (`_tags_for`) so `ingestion.mapping` can attach a `PlaceActivity` edge.
  When constructed with the canonical `app_pack` or `ROEDU_APP_PACK`, it consumes
  redistributable app-pack `venue` items instead. It maps `tags` and `facets.city`,
  `facets.county`, `facets.category`, `facets.venue_category`/`place_category`,
  `source`, and `confidence` into namespaced `raw_tags` while still requiring
  `location.lat`/`location.lon` for ingest.
- `Place` records now store optional source credit fields (`attribution`,
  `license_name`, `provenance_url`) and the RO-EDU adapter maps those fields when
  present. Blank upstream metadata stays blank.

Events:
- `apps/events/management/commands/sync_roedu_events.py` — pulls the `events`
  product, resolves each event to an existing `Place` (geo + name via
  `find_duplicate`), and upserts **facts only** (no scraped `description` — M2) via
  the shared `upsert_event`. Defaults to `--min-confidence 1.0` (JSON-LD/iCal only;
  NER events are held — M5).
  It also supports `--app-pack roedu:social_media_activities_app:events_places:v1` for the same
  redistributable app-pack endpoint. App-pack event items use `id`, `kind=event`, `title`,
  `start_datetime`/`starts_at`, `end_datetime`/`ends_at`, `place_id`/`venue_id`,
  `source`, `license`, `access_type`, `legal_basis`, `gdpr_relevant`,
  `redistributable`, `confidence`, `tags`, and exact `facets`. It consumes category,
  `status`/`lifecycle_status`, cancellation/deletion/tombstone markers, stable venue IDs, source
  first/last/updated timestamps, recurrence, timezone, price range, currency, free/paid flag,
  availability, ticket URL, and immutable pack snapshot identity. Descriptions are always stored
  as `""`; item bodies, generic source URLs, internal paths/checksums, full evidence, and internal
  provenance are not copied into `Event`. The ticket URL uses the existing bounded public event
  link field; the remaining safe facts use dedicated `source_*` fields and read-only API fields.
- Producer category is retained and mapped deterministically to the local activity taxonomy, then
  title-classified only as a fallback. Low-confidence rows are retained for staff review but are
  excluded from all public event surfaces. Cancelled/postponed/removed/moved-online rows are not
  upcoming; moved-online is retained only as source truth because this product is in-person.
- A body-less tombstone can be applied from a legacy `--updated-since` delta. Absence is inferred
  only for a complete, unbounded, well-formed immutable app-pack snapshot, scoped by `(pack_id,
  city)` and committed atomically with its checkpoint. Live app-pack events must reference a served
  venue that resolves to a local `Place`; unresolved relationships are skipped and make absence
  reconciliation unsafe. Partial pages, limits, malformed items,
  legacy snapshots, and deltas never infer absence. Older snapshots fail closed unless an operator
  explicitly uses `--allow-snapshot-rollback` (ADR-0023).
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

Tests never use live network calls. Pure client/adapter cases use synthetic serving fixtures; DB
cases cover persistence, lifecycle reconciliation, API rendering, and child-venue fail-closed
behavior:
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

App-pack fixture/serving-layer path, once the serving endpoint exists:

```bash
ROEDU_API_URL=http://<server-host>:8077
ROEDU_API_KEY=<set-in-environment>
ROEDU_APP_PACK=roedu:social_media_activities_app:events_places:v1
python manage.py ingest_places --source=roedu --city="Cluj-Napoca"
python manage.py sync_roedu_events --city="Cluj-Napoca" \
  --app-pack roedu:social_media_activities_app:events_places:v1
```

The expected HTTP request is:

```text
GET ${ROEDU_API_URL}/v1/app-packs/social_media_activities_app/roedu:social_media_activities_app:events_places:v1?layer=redistributable&city=Cluj-Napoca
X-API-Key: ${ROEDU_API_KEY}
Accept: application/json
```

The consumer tests use synthetic fixtures for that payload and intentionally do not import producer
internals. The wider Phase 0 gate additionally exercises producer → promoted server projection →
consumer validation as a cross-repository contract.

`sync_roedu_events` does NOT use `INGESTION_EXTRA_ADAPTERS` (it instantiates
`RoeduClient` directly), but it depends on step 2 having run, so keep the env set
for the whole sequence.

The scheduled `sync_roedu` wrapper reads `ROEDU_APP_PACK`. When set, venue ingestion and event
sync both use that app pack; when absent, both use legacy products. One run never mixes modes.

## Safety model

The platform enforces the license/GDPR gate **server-side** (the `social-app-dev`
key is redistributable-only, no `tdm_exception`). Treat that as defence-in-depth —
the client also fails closed (`available != true` ⇒ no records).
For app packs, the client rejects any item missing current legal/privacy, policy, capture, and
acquisition attestation or violating its exact facts-only schema. This social app does not request the
internal/all layer over HTTP because no internal/admin/ops scope is proven here.
Redistributable app-pack examples and client-visible payloads must not include
internal artifact paths, internal checksums, internal `llms.txt` entries, internal
source URLs, or TDM-only item bodies.

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
2. **Held-event review UX (M5 follow-up)** — `sync_roedu_events` retains
   `confidence < 1.0` events behind the public gate and exposes them in admin filters, but there is
   no dedicated approve/reject workflow. Redistributable app packs never copy outbound source URLs.
3. **Presentation of machine-readable source facts** — ticket URL is usable through the normal
   event website link and recurrence/timezone/price/free/availability are retained and exposed by
   the read-only API. A dedicated localized price/availability/recurrence treatment on public event
   cards is still deferred; the UI must not guess or expand an upstream recurrence rule.
4. **`amenity=cinema` → `open_air_cinema` is a stopgap** — there is no plain indoor
   "cinema" activity type in the taxonomy; the rule reuses `open_air_cinema` (which
   carries the "cinema" alias) at low confidence. A dedicated `cinema` activity slug
   would be cleaner.
