> **COMPLETED (2026-07-02):** immutable dated changelog of the 2026-06-07/08 session — current
> state lives in `../../STATUS.md`.

# Changelog — 2026-06 session

What shipped in the 2026-06-07/08 session, in order. Each item was built as its own branch and
went through the full per-feature cycle: **map the seams (parallel sub-agents) → implement → run the
Docker test suite + lint/format/migration gates → adversarial-review workflow on the diff → fix every
confirmed finding → merge `--no-ff` → push to origin → apply any migration to the dev DB.**

Test suite grew **969 → 1049** passing over the session; `ruff check` / `ruff format --check` /
`makemigrations --check` clean throughout. All work upholds the hard invariants in `CLAUDE.md` /
`docs/SAFETY.md` (cohort isolation, no adult↔minor private contact, privacy-by-default / no stored
user location, text-first, no engagement-maxxing/vanity metrics).

---

## 1. UI redesign — grouped nav + Inbox/You hubs + fresh design system  (`5e365d5`)

A presentation-layer redesign of the server-rendered web UI (`apps/web/` + `templates/base.html` +
`static/css/base.css`) — "simpler, more intuitive, modern" — with **no behaviour or URL changes** to
the underlying flows (services/gates untouched).

- **Design system** (`static/css/base.css`, rewritten): fresh near-white/airy palette, single green
  accent, spacing/radius/shadow scales, refined components (buttons, cards, chips, tabs, dropdown
  menus, drawer, forms, thread/posts). The dark + high-contrast themes (F12), reduced-motion, and all
  F16 accessibility chrome are preserved; **every new colour token is remapped in all four theme
  blocks** (light, prefers-dark auto, explicit dark, contrast). Muted text darkened to clear WCAG AA.
- **Navigation** (`templates/base.html`): the cramped ~15-link top bar became a grouped bar —
  **Browse · Organize · Discover ▾ · Inbox ▾ · You ▾ · Support** — with a no-JS, keyboard-accessible
  **mobile ☰ drawer** (checkbox-hack, not `<details>`, which fails as an always-open desktop bar in
  modern Chrome's `::details-content`). Nav badge data made robust per-view.
- **Consolidation**: an **Inbox** section (Alerts + Messages + Connections) and a **You** section
  (settings overview at `/you/` + Profile/Interests/Display/Privacy/Donations/Guardians) with shared
  tab strips (`_inbox_tabs.html`, `_you_tabs.html`); `/inbox/` + `/you/` landing routes. Old URLs all
  still work.
- **Correctness/a11y fixes surfaced during the redesign**: 8 **multi-line `{# #}` template comments
  that Django renders as literal junk text** (nav, threads, activity/group pages) → converted to
  `{% comment %}`; thread reaction/mention styles moved out of an inline `<style>` into tokenised CSS
  (they were hard-coded hexes, broken in dark mode); the post-edit form now correctly `:target`-reveals;
  the drawer toggle made keyboard/screen-reader operable.
- Verified by headless screenshots (desktop + mobile, light + dark). Adversarially reviewed (12
  findings, all fixed). **969 tests.**

## 2. F4 — Recurring activity series (templated next-instance respawn)  (`3dd0e0c`)

An organiser defines a repeating meetup once; the platform auto-spawns **only the next single
Activity** through the existing `create_activity` path, so every cohort/consent/blocking gate re-runs
per instance and each instance needs a fresh per-member join (no persistent roster, no attendance
rollup).

- **Model**: `social.ActivitySeries` (immutable `place`/`activity_type`/`cohort`; cadence
  weekly/biweekly/monthly; logistics template; `next_starts_at` cursor + `anchor_day` +
  `duration_minutes`; active/paused/ended) + nullable `Activity.series` FK (`SET_NULL`). Migration
  `social/0015`.
- **Engine**: `spawn_due_series` (in `ops` `DUE_JOBS`) materialises the next instance a lead window
  (`SERIES_SPAWN_LEAD_DAYS`, default 14 d) ahead so members can join before it starts; **one upcoming
  instance at a time**; cohort re-asserted before spawn (**pause on owner cohort drift** — never spawn
  into the wrong cohort); transient eligibility/place loss skips + self-heals; past slots
  fast-forwarded with **no backfill** and **fail-closed** (never a past-dated meetup); per-series
  `transaction.atomic` + `select_for_update(skip_locked)` (no double-spawn on overlapping ticks);
  **DST-correct** cadence math + monthly **anchor-day** clamp; audited; cron-safe.
- **Surfaces**: owner-walled DRF `SeriesViewSet` (`lookup_value_regex`, allowlist serializer — no
  roster/counts) + web create/list/detail/pause/resume/end with owner-only gating + discovery links.
- Adversarially reviewed (18 findings; confirmed ones fixed). **1002 tests.**

## 3. F3 — Saved-search "tell me when a matching meetup appears" alerts  (`126b2e5`)

A user saves a discovery filter and gets **one opt-in in-app alert** the first time a new activity
they could already see matches it. Search/save-only — no suggestions feed, no counters, no digest.

- **New app `apps/saved_searches/`**: `SavedSearch` (cohort PINNED from the user; exactly one of
  `activity_type`/`category`; optional `Area` + `beginners` + `cost_band`; **no stored coordinate** —
  area-only geo) + a `SavedSearchMatch (user, activity)` ledger = the **one-notice-per-pair-ever**
  dedup. Migrations: `notifications/0010` (new mutable `ACTIVITY_MATCH` kind + why-line) +
  `saved_searches/0001`.
- **Matcher**: `match_saved_searches` (in `DUE_JOBS`) fans out **per saver** through the cohort read
  gate (`social.visible_activities` + explicit `status=OPEN`/upcoming, excludes the saver's own),
  fires one `notify(ACTIVITY_MATCH)` per (user, activity); the ledger row is written even when the
  notice is muted (so a muted saver is never re-fired after un-mute); per-saver rate cap + per-tick
  batch cap (anti-flood); per-search isolation; re-asserts cohort **and** `can_participate`; the
  optional city is resolved to an Area only **after** the anti-abuse gates (no write-amplification).
- **Surfaces**: owner-walled DRF `SavedSearchViewSet` (allowlist serializer — no counters) + web
  `/saved-searches/` list/create/delete + nav/You links. A saved search is a discovery filter, never
  a "shared activity", so it opens **no private-contact path**.
- Adversarially reviewed (15 findings; confirmed ones fixed). **1035 tests.**

## 4. F5 — Geography-aware, distance-bounded recommendations  (`dc921f1`)

When the home feed carries request-only coordinates, the recommendation ranking is re-ranked toward
reachable venues so an unreachable "perfect match" isn't shown above a nearby one. **Core only** — no
new model/migration; the embeddings token-enrichment / stored preferred-area work was deliberately
**scoped out** (it would become a location proxy / privacy regression).

- `recommendations.services.recommend_activities`: when `near_point` + `radius_m` are given,
  over-fetch the pgvector cosine ranking (`REC_OVERFETCH=4`) then re-rank in Python by
  `_rec_score = max(0, 1 − cosine_distance) × distance_decay(metres) + (ACCESS_BOOST if the venue
  matches the user's stated access needs)`, where `distance_decay = 1/(1 + m/3000)`. The radius stays
  a **hard filter** (decay only re-orders within it); the similarity base is **clamped ≥ 0** so a
  negatively-correlated match can never invert the distance ordering. **With no coordinates the result
  is byte-identical.** `rec_distance` stays the **raw cosine**, so the displayed % match is honest (the
  blend is a throwaway sort key). Cohort isolation preserved (re-rank only re-orders the gated set).
- The access boost is **additive-only and SOFT** — it only lifts a positive match and never hides or
  down-ranks an unknown-accessibility venue (F15).
- `web.home()` parses a transient request-only point (never stored) + default 10 km radius and appends
  honest reason suffixes ("· near you" within 2 km, "· matches your access needs"); also fixed an F17
  gap (an uncategorised scored match now gets a "{pct}% match" base reason).
- Adversarially reviewed (7 findings; the one real bug — decay inversion on negative cosine — fixed).
  **1049 tests.**

---

## Notes for the next session

- The strongest remaining catalog items that are **not** gated on minor onboarding: **F11** (staff
  moderation triage hints — staff-only, no user-facing surface). **F7 / F9 / F29** are sequenced to
  land *with* `ALLOW_MINOR_ONBOARDING`, not before. See `docs/FEATURE_CATALOG_2026-06.md`.
- Pre-existing **i18n debt** (app-wide, deliberately not addressed here): web-view flash `messages.*`,
  safety-app notification copy, and model-choice labels are still English; only templates + a few
  service labels are Romanian. Holistic i18n is its own feature.
- Go-live work (deploy/config + legal) remains the gate to real users; see `docs/RELEASE_READINESS.md`
  (note: that doc predates this session and several items it lists as open are now closed in code).
