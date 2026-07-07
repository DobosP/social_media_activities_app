# ADR-0020 · Multi-type activities, create wizard, live concept filters, demo events

- **Status:** accepted (owner feedback on ADR-0019 P4/P1, 2026-07-07)
- **Relates to:** ADR-0019 (§1 map, §4 organizer form), W2-F1 vocabulary matching.

## Context

Owner review of the shipped ADR-0019 slices: (a) the `<details>` toggle sections on the
create form don't feel organized — prefer a multi-step flow that can go back and forth;
(b) the activity-type picker is preset-only — people who can't find the right word need to
type and get a similarity list, and an activity can be more than one type; (c) the Locuri
filter should offer the same concept typeahead and apply live to the map; (d) `/events/`
renders empty — diagnosis: the static RO-EDU seed carries 39 REAL events but the newest is
2026-06-29, all past `upcoming_events()`'s `starts_at >= now` (the data-only seed decays by
design; prod freshness is the ADR-0019 §7 daily sync). Viewing capabilities need testable
data cross-device.

## Decision

1. **Secondary activity types (additive M2M).** `Activity.activity_type` (FK) stays the
   PRIMARY type — every existing cohort/envelope/discovery/series contract is untouched.
   New `Activity.secondary_types` M2M (cap 2, active-only, ≠ primary). The guardian
   **category envelope is enforced on EVERY type** (a child cannot smuggle a disallowed
   category in as secondary). Type filtering (browse `?activity=`, API filter, free-text
   search predicate) matches primary OR secondary; cards/detail show secondary chips.
   Saved-search matching keeps primary-only for now (noted follow-up).
2. **Create/edit becomes a stepper, not toggles.** One form, one POST — the page is
   organized as sequential step panels (Ce → Unde → Când & cât → Detalii) with a progress
   header and back/next buttons driven by CSP-safe static JS. No JS = all panels render
   stacked (unchanged semantics); server-side errors reopen the first offending step.
   No formtools/session wizard (state lives in the DOM; nothing new to persist).
3. **Concept typeahead everywhere, client-side.** The 38-type vocabulary (slug, RO name,
   aliases, top-level category, depth-1 synonym links — the same data W2-F1's
   `_matching_type_ids` reads) ships as a nonce'd JSON island; a reusable CSP-safe
   combobox (input + listbox, diacritics-normalized substring/alias matching, chips in
   multi mode) enhances (a) the type picker (primary single + secondary multi) and (b) a
   new places-map concept search that filters the loaded GeoJSON LIVE (name + type +
   category match, composing with the existing chips) — no round trips.
4. **Demo events for view testing.** `generate_demo_events` management command,
   DEBUG-guarded and `source="demo"`-marked: reschedules the seed's real past events into
   the coming weeks (keeps genuine Cluj titles/venues) and synthesizes a few extras to
   cover every display state (multi-day, free/paid bands, each category). Never a
   production path — prod stays on the §7 sync.

## Consequences

- Migration adds one M2M table; no data migration (existing activities have no secondary
  types until organisers add them).
- The type filter becomes an OR across primary+secondary with `distinct()` — the browse
  and API surfaces stay contract-compatible (same param, broader honest matches).
- The wizard replaces the ADR-0019 §4 `<details>` groups on create/edit (they lasted one
  review cycle — the sectioning itself survives as step panels).
