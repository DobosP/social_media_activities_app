# ADR-0060 — Gated event city and place facets

Date: 2026-10-13
Status: source independently reviewed and full21 qualified; owner landing pending
Extends: [ADR-0025](0025-agent-and-search-engine-access-surface.md) and
[ADR-0059](0059-event-city-filters-and-local-poc-history.md).

## Context

Public event queries already support city, venue, activity, date, text and proximity
filters. Consumers cannot discover matching cities or venues without walking every
event page. Counting only the first event page would misrepresent available coverage.

## Decision

- Add anonymous read-only `GET /api/events/facets/` and
  `GET /api/v1/events/facets/`. Both share the existing event query parser and the
  runtime's immutable catalog event policy. All current event API filters compose
  identically, including its existing `include_past` semantics. Unsupported
  `past_only` and `area` parameters acquire no new meaning here.
- One PostgreSQL statement materializes the full admitted event query, then counts
  events and aggregates city and place buckets before limiting either bucket page.
  Counts and pages share one read snapshot. Held/tombstoned events, unpublished
  proposed venues, closure overlays and invalid runtime policy retain their vetoes.
  No engagement, participation, user or child counts exist.
- City buckets use the same NFC, Unicode-whitespace, Romanian spelling and Bucharest
  alias key as city filtering. The label is the byte-order minimum actual stored
  city string among matching events, using PostgreSQL's `C` collation. It is a display
  fact, not a guessed canonical spelling; stored city values remain unchanged.
- Venue buckets retain the existing public place ID, name, stored city and comparison
  key. Equal names or coordinates never merge IDs. Missing venue events are counted
  in `missing_place_count`; missing or whitespace-only city events in
  `missing_city_count`, which includes missing venues. Empty cities have no city
  bucket, while known venue IDs with empty cities still have venue buckets.
- Response `count` is the full matching event count. `cities` and `places` each have
  `count` (total buckets), `results`, `limit`, `offset`, `complete` and `truncated`.
  Existing pagination bounds apply independently to both pages: default 50,
  maximum 200, offset capped at 1,000,000. Buckets sort by event count descending,
  then byte-order city key or numeric place ID. These are inventory counts.
  `complete` means the returned page contains all buckets; `truncated` is its inverse.
  A final nonempty page can still be truncated because earlier buckets are omitted.
  Empty matching queries return complete empty arrays. Clients advance each offset
  by the returned limit; separate requests can observe later data.
- Matching POC events or POC venues mark the aggregate `local_poc=true` and include
  the existing `LOCAL POC — not cleared for publication` notice. Production responses
  have `local_poc=false` and a null notice. No schema migration or POC activation is
  introduced. Raw provenance, source paths, event IDs and account data stay outside
  this exact additive projection; original event/place responses remain unchanged.
- OpenAPI adds only the new routes and response schema. GUI, feeds, snapshots,
  ingestion, child venue decisions, release selection and production remain unchanged.
  App `main` landing/pushing remains the owner's action.

## Verification

Synthetic registered-route database tests cover more than 200 event/city/venue
buckets, continuation/caps, alias grouping with untouched labels, missing facts,
all supported event filters, literal search wildcards, history compatibility,
publication vetoes, configured closure thresholds/decay, invalid policy and POC
labels. Invalid requests fail before database access. Additive OpenAPI tests preserve
legacy parameter maps, with route inventory and actual documentation checks.

Exact executed gates and remaining checks are recorded in `WORKLOG.md` and `STATUS.md`.

Independent source review completed before publication (see the
[event-facets source review](../reviews/native-go/event-facets-independent-review-20261013.json)).
The first full21 integration attempt then found an app documentation test still
asserting380 operations/318 paths. Its exact inventory expectation was corrected
to the additive382/320 contract, without changing API behavior or other assertions.
The isolated app lane passes35 tests/0 skips/0 failures. A fresh full21 check on the
corrected head and owner app-main landing remain pending. No new production image
or image-audit result is claimed.

Full21 closure (2026-10-13): corrected source
`37129450d27990c6f7620bb72155ddbc8a094a20` completed747 unique top-level passes
across all21 native PostgreSQL/codec lanes,0 failures/0 skips. The
[full checkpoint](../reviews/native-go/event-facets-full21-checkpoint-20261013.json) and
[independent recount](../reviews/native-go/event-facets-full21-independent-review-20261013.json)
bind the exact source and genuine first-failure history. This supersedes the prior
pending-full21 verification status above. Source behavior is unchanged by the app
assertion correction and this evidence-only closure. Historical native31/Node4
evidence remains separately scoped; no new canonical production image/audit,
Social runtime activation or owner app-main landing/push is claimed.
