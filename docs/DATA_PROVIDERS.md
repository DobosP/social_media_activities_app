# Data providers — places & events

A living registry of sources to extend the place/event dataset. Goal: **free** data
(open bulk downloads or free-key APIs), Romania/Cluj-relevant, fitting our existing
seams (`ingestion.sources.SourceAdapter` for places, `events.sources.EventSource` for
events, the enrichment path for live data). All place/event data is non-personal and
each source's **attribution/licence** is respected (recorded via `Place.source` /
`raw_tags`). No behavioural/personal scraping — consistent with [SAFETY](SAFETY.md).

Legend: 🟢 free, no key · 🔑 free with a key/token · 💳 paid.

## Places — open data (bulk coverage)

| Provider | Gives | Licence | Pass | Status / fit |
|---|---|---|---|---|
| OpenStreetMap / Overpass | parks, libraries, archives, pitches, halls, venues + website/phone | ODbL | 🟢 | **integrated** (`OverpassAdapter`) |
| OSM bulk — Geofabrik (Romania) | whole-country OSM in one file | ODbL | 🟢 | **planned P2** (`GeofabrikAdapter`) |
| Overture Maps — Places | ~60M POIs + categories/websites | CDLA-Permissive (some ODbL) | 🟢 | **integrated** (`OvertureAdapter`) |
| Foursquare OS Places | 100M+ POIs, monthly | Apache-2.0 | 🔑 `FSQ_PLACES_TOKEN` | **planned P2** (parquet/DuckDB) |
| Wikidata (SPARQL) | institutions/parks/venues + official website | CC0 | 🟢 | **planned P2** (enricher) |
| data.gov.ro | RO public datasets (sport clubs, etc.) | OGL-RO | 🟢 | per-dataset adapter |
| OpenData CJN (Cluj) | hyperlocal city data | open | 🟢 | per-dataset adapter (launch city) |

## Places — free-tier APIs (enrichment / geocoding)

| Provider | Gives | Pass | Status |
|---|---|---|---|
| Google Places (New) | open-now, rating, website, phone | 🔑 `GOOGLE_PLACES_API_KEY` (💳 free credit) | **integrated** (flag-gated enricher) |
| OpenTripMap | tourism/POI (OSM-derived) | 🔑 free key | optional |
| Geoapify / LocationIQ | geocoding | 🔑 free tier | optional |

## Events — open / no-key (backbone)

| Provider | Gives | Pass | Status |
|---|---|---|---|
| iCalendar (.ics) feeds | library/theatre/uni/municipal programmes | 🟢 | **integrated** (`ICalFeedSource` + `ingest_events`) |
| data.gov.ro / Cluj portal | official local happenings | 🟢 | per-dataset `EventSource` |

## Events — free-key APIs

| Provider | RO value | Pass | Note |
|---|---|---|---|
| Ticketmaster Discovery | concerts/sports | 🔑 `TICKETMASTER_API_KEY` | **planned P2**; best free coverage |
| Eventbrite | community events | 🔑 OAuth | public search restricted → limited |
| SeatGeek | live events | 🔑 free | US/Canada-centric |
| PredictHQ / Meetup | aggregated / groups | 💳 | paid; deferred |

## Keys to obtain (free signups) — see chat instructions

1. **Ticketmaster Discovery key** — developer.ticketmaster.com → My Apps → Consumer Key.
2. **Foursquare OS Places token** — opensource.foursquare.com/os-places → Places portal.
3. **Google Places key** *(optional, needs billing w/ free credit)* — Google Cloud →
   enable "Places API (New)" → API key.

Hand them over as env vars (`TICKETMASTER_API_KEY`, `FSQ_PLACES_TOKEN`,
`GOOGLE_PLACES_API_KEY`) — never committed. The 🟢 sources (Wikidata, Geofabrik,
data.gov.ro, Cluj, OSM, Overture, .ics) need no key and can be built immediately.

## Classification

Collected places map to the activity taxonomy via `ingestion.mapping` (OSM tags) and
the Overture category map; incoming events are auto-classified to activity types by
`events.classify` (matching taxonomy names/aliases, e.g. "Maratonul…" → `marathon`,
"Zilele Clujului" → `city_day`).
