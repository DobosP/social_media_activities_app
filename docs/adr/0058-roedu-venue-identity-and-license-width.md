# ADR-0058: RO-EDU venue identity wins over location; item license up to 500 runes

Date: 2026-10-12
Status: accepted (owner directive 2026-10-12: "Even if they have same location, it may be two
different venues" and "increase the server limit"); landing per ADR-0040
Builds on: [ADR-0024](0024-canonical-roedu-social-app-pack.md) (canonical pack) and
[ADR-0055](0055-roedu-policy-schema5-ruleset8.md). Changes neither pin.

## Decision

**Venue identity.** One RO-EDU venue id is one place. Two RO-EDU venues with different ids stay
two places, each keeping its own events, even at the same coordinates or with the same name. For
example, Opera Națională Română Cluj and Teatrul Național Cluj share one building. Location and
name similarity still *nominate* a merge. They never *decide* one between two venue ids:
- A place's RO-EDU identities are its own `external_id` when `source='roedu'`, plus every
  `merged_sources` entry with `source: roedu` (`jobs.RoeduVenueIdentitiesSQL`). No place may ever
  carry more than one.
- Both RO-EDU place writers, `sync_roedu` and `ingest_places source=roedu` (`upsertPlace`), merge
  only through `jobs.FindRoeduDuplicate`. It still merges a venue into a nearby (≤75 m),
  name-similar (≥0.82) non-RO-EDU place, but only into one that no *other* venue id has claimed.
  The chosen row is locked `FOR UPDATE` and rechecked first. Two syncs for differently spelled
  city facets ("Bucuresti"/"București") hold different advisory locks, and the two writers hold
  different ones again, so they could otherwise both claim the place. A venue that loses gets its
  own `source='roedu'` place. A claim is persistent: the place a venue already claimed is
  preferred over any nearer newcomer, so re-syncs and re-ingests keep the same mapping.
- `dedup_places` skips any candidate pair whose union of identities has more than one venue id
  (both rows are read fresh on every iteration). `mergePlaces` repeats the check under its row
  locks for every caller. `aggregate_unnamed_places` merges only unnamed OSM/Overture sub-venues
  into sports/park/school parents, which never carry RO-EDU identities, but it is covered by the
  same guard.
- Database constraints are unchanged. The only place uniqueness is `(source, external_id)` and the
  OSM identity, and nothing in the schema is keyed by location or address.

**License width.** The consumer accepts an item `license` of up to 500 runes, which is the
ro_data_server bound (`legal_basis` already had 500). This applies to the native validator and
to the legacy Python oracle validator, which the validator golden pins. `places_place.license_name`
and `events_event.license_name` widen from `varchar(120)` to `varchar(500)` in `schema.Migrate`
(version `go-roedu-license-500-v1`). The migration is catalog-only, guarded and idempotent.
`places_placecover.license_name` (Commons covers) keeps 120. The admin curation field for
`events.event` `license_name` (manual events; imported facts are not editable there) is 500 too.

## Context / why

The real `opera_cluj_events` legal basis is 131 runes. Under the 120-rune bound its venue and all
18 events were refused as malformed, the same way the server withheld them. Widening only the
validator would have made the INSERT fail with "value too long". That would abort the whole
city's sync transaction, so the columns widen together with the validator.

The previous merge path let any number of RO-EDU venues fold into one OSM place within 75 m. Two
venues in one building then shared a single place and its events. `dedup_places` could also
delete one of two event-less RO-EDU places that had one name and one location.

Rejected:
- **Disable cross-source merging for RO-EDU.** This would duplicate every venue OSM already
  knows, and nothing in the directive requires it.
- **Key by address.** A shared building is exactly the case where the address is shared too.
- **Truncate the license to 120.** The license is a credit/legal string, and truncating it would
  silently misstate the legal basis.

## Consequences

- When several RO-EDU venues fit one OSM place, the first venue in pack order claims it and the
  others get their own places. "Best match wins" is not attempted.
- A place that already carries two identities, from data written before this change, refuses
  every further merge. It is never split automatically.
- The Django oracle models and their `[:120]` slices in `ro_scraper.py`/`sync_roedu_events.py`
  stay at 120. The oracle is offline and truncates rather than withholds. Widening it needs Django
  migrations, so that is deferred to the oracle's next requalification.
