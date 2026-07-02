# Data sources & integrations

Strategy for the two "external data" questions from the brief: **(1)** how to collect as much
place information as possible so users rarely create places, and **(2)** whether/how to make
**bookings through the app** via providers' REST APIs. See [ROADMAP](ROADMAP.md) D1/D7/D8 and
[ARCHITECTURE](ARCHITECTURE.md) (adapter seams).

> **As-of note (2026-07-02, May-era doc):** the source landscape has grown since this was
> written — **RO-EDU** (`romania_scraper` data API) is now a wired places/events ingestion
> source ([ROEDU_INTEGRATION](ROEDU_INTEGRATION.md), 2026-06); the live provider registry
> (Foursquare/Ticketmaster/Wikidata/Geofabrik etc.) is [DATA_PROVIDERS](DATA_PROVIDERS.md);
> and the events pipeline (`apps/events/`, iCal + RO-EDU sync) shipped. The strategy below
> (free-first, adapter seams, D8 phasing) still stands.

## Guiding rule: free-first, compute-efficient, cheap

We're a donation-funded nonprofit, so: **open data first**, paid APIs only where they clearly earn
their keep, and ingestion designed to be light (one combined query per area, idempotent re-runs,
no tight polling).

## Place data sources

| Source | Cost | Coverage | Role | Status |
|---|---|---|---|---|
| **OpenStreetMap (Overpass)** | Free | Excellent in EU; machine-readable `opening_hours` | **Primary** seed | ✅ D1 |
| **Overture Maps** | Free / open (Parquet via DuckDB) | ~60M POIs worldwide, normalized | Bulk baseline / second source | 🧊 D7 (stub exists) |
| **Google Places** | Paid (per-request) | Most comprehensive; live status, popular times | **Enrichment only** (open-now, links) | 🧊 D7, optional |

### How ingestion works (D1, today)

`SourceAdapter` → `RawPlace` → `ingest_places` command → `update_or_create(Place)` +
`PlaceActivity` edges via the OSM-tag→activity **mapping** (`apps/ingestion/mapping.py`). Scoped to
one administrative area (city) at a time; **idempotent**; user-confirmed/manual edges are never
clobbered. See repo `README.md` for commands.

### Adding sources later (D7)

- **Overture:** implement the stub adapter — DuckDB `read_parquet` over the Overture places
  release, filter to a bbox, map `categories.primary` → our activity slugs, yield `RawPlace`.
- **Google Places enrichment:** *don't* re-import places from Google; instead enrich existing ones
  with **open-now / live status**, popular times, and official links where the spend is justified.
- **Cross-source dedup/merge:** keyed off `source` + spatial proximity + name similarity; keep
  `raw_tags` for provenance. Out of scope until we have >1 source.
- **`opening_hours` parsing:** D1 stores the raw OSM string; D7 parses it to the structured JSON
  field for "open now" queries.
- **Status / events / suggestions:** "is something happening there" comes from live-status
  enrichment + an events association; **area suggestions/recommendations** traverse the activity
  graph and use interest similarity (`pgvector`).

## Booking integration (D8)

**Reality check:** there is **no universal booking standard** — it's fragmented per provider.
Some venue/facility platforms expose REST APIs and there are sports-booking aggregators, but
coverage and schemas differ widely. So we phase it:

1. **Deep-links first.** For every place, surface "how to book" (provider link / instructions).
   Zero integration cost, universal coverage. Ships as the baseline.
2. **`BookingProvider` adapter interface.** Same pattern as `SourceAdapter`: a common interface
   (`availability()`, `create_booking()`, `cancel()`), one implementation per provider.
3. **Per-provider REST integrations**, prioritising the **largest Romanian** venue/facility
   providers and any aggregators that cover several venues at once (best coverage per integration).
4. **Map bookings to activities** so a meetup can carry a real reservation.

**Definition of done (D8):** at least one provider supports in-app booking tied to an activity;
everything else falls back to deep-links. Expand provider-by-provider as partnerships allow.

### Open questions for D8

- Which Romanian providers/aggregators have usable APIs, and on what commercial terms (a nonprofit
  may get goodwill access)?
- Auth & liability model for making bookings on a user's behalf.
- Handling cancellations/no-shows and keeping availability fresh without heavy polling.
