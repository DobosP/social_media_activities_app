# ADR-0019 · Places v2 + information-architecture redesign

- **Status:** accepted (owner direction, 2026-07-07)
- **Date:** 2026-07-07
- **Relates to:** ADR-0016 (React migration program — this is the "later program" it deferred),
  ADR-0007 (one contextual cover photo + generated fallback), ADR-0002 (cohort connections),
  D1/D7 place pipeline, `docs/SAFETY.md`.

## Context

Owner review (2026-07-07) of the shipped product found: the Places map looks dated (raster
Leaflet, no filters, name-only popups); places have no images and no topic grouping; the
place detail page is an information bomb that leads with "not recorded" noise; chat is two
clicks deep inside an Inbox subtab; connections hide in the same subtab; the "Support" link
sits in the primary nav; the organizer form shows ~20 fields at once, includes a
"getting home" field and a two-field Plan-B "fallback" that don't pull their weight, can't
express a concrete cost, and can't reference a place that isn't in the catalog yet.

Recon facts this decision builds on (Codex recon 2026-07-07, anchors in the recon reports):

- `/places/` map = vendored Leaflet + `tile.openstreetmap.org` rasters, plain markers, no
  clustering, no UI filters; fetches `/api/places/?page_size=500` (`static/js/places-map.js`).
- `Place` already has: PostGIS point, address fields, parsed opening hours + open-now,
  `website`, `phone`, `source` enum (`osm/overture/google/user/roedu`), `osm_id`,
  `raw_tags` (Overpass tags incl. `image` / `wikimedia_commons` / `wikidata` where present),
  attribution/license/provenance, `PlaceActivity` edges with origin/confidence/disputed.
  **No image field.**
- User place proposals are LIVE (web + API): quorum 3, proposer excluded, pending visible
  to proposer/staff only. Corrections + closure reports also live with their own quorums.
- The roedu lane exists (`ingest_places --source=roedu`, `sync_roedu_events`, facts-only M2
  rule) but is **not scheduled** in `DUE_JOBS`; the scraper's `venues` product is minimal
  (no images/categories/official URL) and its API has no changed-since delta.
- Notifications are in-app only; `activity_updated` already fires on start-time changes.
  Editing can change logistics/time but **not place**; `fallback_starts_at` is a one-shot
  Plan-B time and `fallback_meeting_point` a Plan-B spot.
- Google Maps Platform: free tier is 10k events/SKU/month (tiles 100k) — workable but
  capped; **Places photos may not be cached/stored (ToS §3.2.3b)**, so any Google imagery
  is rented, not owned. OpenFreeMap (MIT, keyless, OSM vector tiles, MapLibre-native) is
  free and production-proven.

## Decision

### 1. Map: MapLibre GL + OpenFreeMap, filters, topic groups

Replace the Leaflet raster map on `/places/` (and later the form place-picker) with
**vendored MapLibre GL JS (CSP build)** + **OpenFreeMap** vector tiles (`liberty` style).
Zero cost, no API key, attribution rendered automatically. The CSP build ships a separate
worker file so `worker-src` stays `'self'` — no `blob:` allowance; CSP gains
`connect-src https://tiles.openfreemap.org` (style JSON, glyphs, tiles).
Google Maps is rejected as the base map (usage caps + lock-in); optional Google enrichment
per D7 remains unchanged.

Map UX: marker clustering (native GeoJSON cluster source); a CSP-safe chip row filters by
**topic group** (top-level taxonomy categories aggregated from the place's `PlaceActivity`
edges — sport, games, reading, culture, outdoor…), **Happening soon** (place has an
upcoming activity or event), and **Open now** (existing parsed-hours evaluation). The
GeoJSON API adds `categories`, `has_upcoming`, `open_now`, `image_thumb` properties and a
`category` filter — additive, backward compatible.

### 2. Place images: own what we store, generate the rest

One image per place (mirrors ADR-0007's one-contextual-photo discipline; this is venue
context, not an engagement surface). New fields on `Place`: `image`, `image_source`
(`wikimedia|business|none`), `image_attribution`, `image_license`, `image_source_url`.

Resolution ladder (idempotent `resolve_place_images` command):
1. `raw_tags.wikimedia_commons` / `raw_tags.wikidata` (P18) / `raw_tags.image` when it
   points at Commons → fetch via the Commons API, store the thumb in our S3 with
   attribution + license from `extmetadata`. Commons licenses permit cached redistribution
   with attribution; hot-linking is rejected (leaks user IPs to a third party).
2. Verified business partners may upload one image through the D6 media pipeline
   (scan + EXIF strip + moderation).
3. Otherwise a **generated cover**: deterministic category gradient + activity glyph,
   class-driven at render time (same pattern as the ADR-0007 accent fallback). No storage.

**Rejected:** Google Places photos (no-caching ToS makes them unstorable), scraping images
in `romania_scraper` (licensing risk, LARGE effort), user photo submissions for places
(moderation load; may revisit once business uploads prove the pipeline).

### 3. Navigation: chat first-class, alerts to a bell, connections on the profile, donations calm

- Mobile tabbar becomes **Home · Browse · Organize · Chat · You**; Chat links straight to
  `/messages/`. The Inbox tab (and its subtab page) is retired.
- A **bell icon** in the header (all viewports) carries the existing unread-notifications
  pill and opens `/notifications/`.
- **Connections** leave the inbox: the profile's existing connections card is promoted
  (pending-request banner + count, up to 8 faces, "see all"), and the avatar menu gains a
  Connections entry. `/connections/` itself is unchanged.
- **Donations**: "Support" leaves the primary nav. It remains one click away via a footer
  "Support the platform ♥" link, an avatar-menu entry, and the existing You → Giving tab.
  Donations-only funding stays visible — just not in the eye-line. (Invariant 2 untouched.)

### 4. Organizer form v2: progressive disclosure, real cost, movable activities

The create/edit form becomes sectioned with native `<details>` groups (CSP-safe, zero JS):
**Essentials** open by default (place picker, activity type, title, start; `supervised`
stays here for CHILD owners), then collapsed **Schedule & size** (end, capacity,
min-to-go), **Cost**, **Accessibility & welcome** (accessibility notes, beginners,
first-time note, difficulty), **Member logistics** (meeting point, what to bring,
organizer note).

- **Removed from the product**: `getting_home_note` (owner decision — including its
  guardian-context mirror and reminder line) and both fallback fields
  (`fallback_starts_at`, `fallback_meeting_point`) plus the one-shot `invoke_fallback`
  affordance. DB columns stay for now (dropped in a later cleanup migration); forms,
  templates, reminders, and console chips stop referencing them.
- **Replacement for "fallback"**: organizers of an open, not-yet-started activity can now
  **move it** — change `place` and/or `starts_at` — via a dedicated service path with the
  same public-place validation as creation. Members are notified (`activity_updated`:
  "…moved to X / now starts Y") and the change is audited (`activity.moved`). This is
  strictly more capable than the one-shot Plan-B latch.
- **Cost, concretely**: `cost_band` stays; new `Activity.cost_amount` (decimal, RON) and
  `cost_note` (what it covers), valid only when the band is low-cost/paid. Cards and
  detail show "≈ 25 RON — echipament inclus" instead of a bare "Paid".
- **Places that don't exist yet**: the place picker gains an inline "Can't find it? Add
  it" mini-flow that files the existing PlaceProposal without leaving the form. **Adult
  cohorts** may immediately attach the activity to their own pending proposal (detail
  shows the pending-venue banner; publication still needs the quorum). **Child cohorts
  keep the published-places-only rule** — new venues must be community-confirmed before a
  child activity can happen there (core safety promise; see SAFETY.md).

### 5. Place pages: progressive disclosure, no "not recorded" noise

Place detail reorders to: hero (cover image or generated cover, name, topic chips,
open-now badge, address, official-website button, partner line) → "Organise here" CTA →
Upcoming & events → collapsed **Community facts** (only recorded accessibility/venue facts
surface by default; "not recorded" rows and confirm/dispute/vote controls live inside) →
collapsed **Data & corrections** (source credit, correction quorums, closure report,
claim-this-venue). The places list drops "limited/not recorded" badge noise (positive
"matches your access needs" badge stays).

### 6. Businesses become first-class partners (portal, not ads)

`Partner.kind` gains `business`. New `PlaceClaim` model + flow: a venue owner claims their
place ("Is this your venue?" in the detail's data section) → staff verify (manual, CUI
optional) → approval links a verified business Partner to the place, unlocks the official
panel (website/phone maintained by the venue, one uploaded image) and an "Official venue
page ✓" badge. **Hard lines kept:** no ranking boost, no featured placement, no ads, no
payment — a claimed business renders through the exact same components as every other
place. Partners page copy widens to "civic & venue partners".

### 7. Place data strategy: four provenance lanes, one store, scheduled freshness

The app's PostGIS `Place` store stays canonical. Lanes: (a) `osm`/`overture` bulk
re-ingest; (b) `roedu` scraper venues + events — **now scheduled**: a `sync_roedu` due-job
(guarded on `ROEDU_API_URL`) runs `ingest_places --source=roedu` + `sync_roedu_events` on
the daily tick, keeping the facts-only M2 rule; (c) `user` proposals through the existing
quorum + the new inline organizer flow; (d) business claim enrichment as an overlay with
its own provenance. Existing dedup (`find_duplicate` geo+name), correction quorums,
closure reports, and last-seen sweeps are the freshness/trust machinery — no new
moderation surface needed beyond the claim review.

Producer follow-ups (separate repos, not blockers): `updated_since` delta on the dataapi
products; venue `category` + official `url` fields in the scraper's `venues` product.

## Consequences

- Map cost stays €0 at any realistic scale; the only new third-party runtime dependency
  is OpenFreeMap tile fetches (self-hostable escape hatch documented upstream).
- Two CSP changes (tiles connect-src; vendored worker file) — the unpkg-removal slice is
  unaffected.
- `getting_home_note` data already entered by organizers stops rendering; columns are
  retained until a cleanup migration confirms nothing references them.
- Child-safety surface unchanged except deliberately: pending-place activities are
  adult-only; guardian "getting home" line retires with the field (owner decision).
- The scraper's minimal `venues` product is consumed as-is; richer producer fields arrive
  later without consumer changes (upsert is field-tolerant).

## Phasing (each slice its own branch, lands on a green gate)

P1 map v2 · P2 image model + resolver + generated covers · P3 nav IA · P4 organizer form
v2 + move-activity · P5 place detail/list declutter · P6 business claims · P7 sync_roedu
due-job + docs alignment. P1/P3 are independent; P5 depends on P2; P6 depends on P5.
