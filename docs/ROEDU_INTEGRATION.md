# RO-EDU integration — places & events from `romania_scraper`

This app consumes the **RO-EDU data platform** (`romania_scraper.dataapi`) as one
more ingestion source. Scope: **Cluj-Napoca first** (matches `INGEST_DEFAULT_CITY`),
national later. Full design: `../roedu/docs/ROEDU_INTEGRATION_DESIGN.md` (§4, §11).

## What's wired on this branch (`feat/roedu-ingestion`)
- `apps/ingestion/sources/roedu_client.py` — vendored stdlib HTTP client for the
  data API (no new dependency).
- `apps/ingestion/sources/ro_scraper.py` — `RomaniaScraperAdapter` reading the
  `venues` product → `RawPlace`. Synthesizes OSM-style tags from the venue name so
  `ingestion.mapping` attaches a `PlaceActivity` edge.

Enable it (no code change):
```python
# config/settings: INGESTION_EXTRA_ADAPTERS = {"roedu": "apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter"}
```
```bash
ROEDU_API_URL=http://<scraper-host>:8077 ROEDU_API_KEY=social-app-dev \
  python manage.py ingest_places --source=roedu --city="Cluj-Napoca"
```

## Still TODO on this branch (tracked from design §11)
1. **Events ingest** — accept `source="roedu"` (B3): add a `SCRAPER`/`roedu` value
   to `Event.Source` **and** the `BatchEventsView` allowlist; then a nightly job
   pulls the `events` product and POSTs `/api/ingestion/batch-events/`
   (`external_id` = `roedu:<dedup_key>`, `match-place` first). Needs a
   `makemigrations events` (CI gate).
2. **Child-venue safety (M4)** — keep `source="roedu"` OUT of the OSM child-venue
   branch (already the case → fail-closed UNKNOWN); add a curated `ChildVenueClass`
   allowlist for scraped cultural venues + a regression test asserting
   `is_child_safe_venue` is False for an unmapped `roedu` place.
3. **Scraped-event gating for minors (M5)** — auto-publish only `confidence==1.0`
   events; hold NER events for staff review; don't surface outbound URLs to
   CHILD/TEEN.
4. **No copyrighted prose (M2)** — ingest event *facts* + `source_url` only; drop
   `description` unless the source is open/public-domain.
5. **Attribution (M3)** — add `attribution`/`license_name` to `Place`/`Event`;
   render the credit (CC-BY/SA sources exist upstream).
6. **Activity-type mapping** — add a rule in `apps/ingestion/mapping.py` for the
   synthesized venue tags (`amenity=theatre/museum/...`) so edges resolve.

The platform enforces the license/GDPR gate server-side (the `social-app-dev` key
is redistributable-only, no `tdm_exception`), but treat that as defence-in-depth,
not the only gate.
