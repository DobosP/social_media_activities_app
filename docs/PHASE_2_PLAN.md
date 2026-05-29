# Phase 2 plan — from "built" to "launched & loved"

Phase 1 delivered the **whole product engine**: identity/age-cohorts + consent, the
social core (activities, threads, join-by-vote, guardian-accompanied child activities),
safety/moderation, chat, media, booking, donations/ops, a rich place dataset, and an
events pipeline. All of D1–D9 plus enhancements are merged to `main` with a green gate.

Treat the current codebase as **the source of truth for the product's intent** — the
models, seams, and `docs/SAFETY.md` invariants encode the agreed scope. Phase 2 does
**not** re-litigate that; it makes the product *usable, discoverable, compliant, and
live*.

## The unified scope (what the app is)

A nonprofit, text-first, **safe-by-design** way for people — **children first, with a
parent/guardian where needed** — to meet **in person** for **real, healthy, group
activities**: sport and endurance/outdoor (running, marathons, hiking, cycling),
fitness, and **participatory culture** you join with others (festivals, city days,
concerts, workshops). The app already **knows the places** (parks, libraries, sports
venues, archives, reservation-capable businesses) and **what's happening** (events),
so a user's job is just to *find people and go*.

Everything in Phase 2 must serve that sentence and uphold the invariants in
[SAFETY](SAFETY.md): age-cohort isolation, verified+consented participation, no
adult↔minor private contact, private-by-default, no ads/tracking.

## How to work (unchanged contract)

Same rules as [MULTI_AGENT_BUILD](MULTI_AGENT_BUILD.md) / [WORKBOARD](WORKBOARD.md):
one track per session on its own `claude/<track>-<slug>` branch; **claim your row on
the WORKBOARD in your first commit**; stay in your owned paths; keep shared files
(`config/settings/base.py`, `config/urls.py`, `requirements*.in`) **append-only**; make
`ruff`, `ruff format`, `makemigrations --check`, `pytest`, `pip-audit` green before a
PR; the integrator merges in dependency order.

## Tracks

### P1 · Discovery & feed API  *(highest leverage; unblocks P3)*
Tie places + events + activities together into the screens a user actually sees.
- New `apps/discovery/` (read-only views; no new core models).
- Endpoints: **"near me"** (places by activity + distance, reusing the PostGIS
  proximity query), **"what's happening"** (upcoming events near me / by activity),
  **"bookable near me"** (`is_bookable` + booking deep-link), **"places with upcoming
  events"** (join places↔events).
- Respect cohort isolation and blocking on anything user-generated; place/event data
  is public.
- Filters: `?activity=`, `?wellness=true`, `?family_friendly=true`, `?near_lon/lat`,
  `?radius_m`.

### P2 · Live data adapters  *(needs the free keys)*
Turn the provider registry ([DATA_PROVIDERS](DATA_PROVIDERS.md)) into running code.
- `FoursquareAdapter` (parquet/DuckDB, mirror `OvertureAdapter`) — token `FSQ_PLACES_TOKEN`.
- `TicketmasterEventSource` (Discovery API → `RawEvent`) — key `TICKETMASTER_API_KEY`.
- `WikidataEnricher` (SPARQL; CC0; backfill official website/links) — **no key**.
- `GeofabrikAdapter` (Romania `.osm.pbf` bulk) — **no key**.
- All behind settings/flags; reuse `SourceAdapter` / `EventSource` seams; attribution
  recorded per source.

### P3 · Recommendations  ✅ built — `apps/recommendations/`
"Activities for you nearby." Interest similarity via `pgvector`, ranked over a user's
cohort + past memberships. No behavioural tracking — recommend from declared interests
and joined activities only.
- `UserInterest` (declared, by activity-type slug) + `ActivityEmbedding` (a `pgvector`
  vector feature-hashed from the activity taxonomy — deterministic, PII-free; kept in
  sync by a post-save signal).
- `recommend_activities` ranks cohort-visible, not-yet-joined, upcoming activities by
  cosine distance to the user's taste vector; cold-start falls back to soonest-first;
  optional `?near_lon/lat&radius_m` proximity filter.
- API: `GET/PUT /api/recommendations/interests/`, `/interests/options/`,
  `GET /api/recommendations/activities/`. **Prod note:** the Postgres `vector` extension
  must be available (the migration runs `CREATE EXTENSION vector`).

### P4 · Notifications
Opt-in, privacy-respecting: "your join request was approved", "an event you follow is
soon". `apps/notifications/` with a pluggable channel (email/web push later); **no**
engagement-maxxing, no tracking. Per [SAFETY](SAFETY.md), nothing that enables
adult→minor outreach.

### P5 · Compliance & legal  *(launch-blocking; human-in-the-loop)*
Finalize DPIA, Privacy Policy, Terms, DSA Art. 28 alignment, consent UX/records review.
Mostly `docs/` + small consent-flow wiring. Pairs with the DPO.

### P6 · Localization (RO/EN)
IS-7 from the roadmap: Django/DRF i18n, `locale/` catalogs, translate user-facing strings;
RO first (launch city is Cluj-Napoca).

### P7 · Security review
Threat-model pass, rate-limit coverage across write endpoints, dependency/audit review,
and pen-test fixes. Gates the public beta (D9 definition of done).

## Suggested parallelization

Run **P1, P2, P5, P6** concurrently first (disjoint paths, no inter-deps except P2's
keys). Add **P3** once P1 lands, **P4** anytime, **P7** before the public beta.

## Definition of "Phase 2 done"
A cohort-appropriate user in Cluj-Napoca opens the app, sees **relevant nearby places
and upcoming events** for the activities they care about, joins a group (a child with
their guardian where needed), and the service is **localized, compliant, security-
reviewed, and deployed**. That is the public-beta bar from [ROADMAP](ROADMAP.md) D9.
