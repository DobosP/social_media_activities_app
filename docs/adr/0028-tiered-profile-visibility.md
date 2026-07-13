# ADR-0028: Tiered profile visibility + person hover cards

Date: 2026-07-13 · Status: accepted (owner-approved tier matrix)

## Context

The owner asked for a system defining how a profile is visible to people connected /
not connected / inside shared groups and activities — keeping the services-first
architecture, with the image (avatar) as a MUST on every person reference, plus a hover
overview. No other-user profile page existed; other-user data exposure was a patchwork of
per-surface ad hoc gates, and the activity roster was the widest-open surface (visible to
any same-cohort viewer of the page, not block-filtered — unlike `group_roster`).

## Decision

One relationship-tier resolver (`apps/connections/profiles.py`), derived LIVE per request
from existing primitives — no stored relationship labels/rollups (inv.2):

1. **Vetoes first, indistinguishable from nonexistence (404):** blocked either way,
   cross-cohort, UNASSIGNED cohort, inactive target, self (self redirects to `/profile/`).
2. **CONNECTED** — accepted connection (`are_connected`).
3. **SHARED** — current peer co-membership of an Activity (`shares_activity`, guardian seats
   never count) or a standing Group (new `shares_group`), or a **pending join request**
   between requester and organizer (owner decision: serves the "should I admit this person"
   review).
4. **STRANGER** — same cohort, no shared context (owner decision: gets the minimal card,
   exactly the docs/SAFETY.md cap — display name + generated avatar, nothing else).

Field matrix (owner-approved): SHARED adds username handle, age-verified badge (boolean
only), the shared context itself (≤3 titles + counts of SHARED memberships only — never
history), and the Connect affordance. CONNECTED adds the Message affordance and, for adults
only, declared-interest chips + the uploaded photo on the profile PAGE (existing
`can_view_photo` cohort gate re-checks at signing). **Minor clamp:** when the pair is in a
minor cohort, CONNECTED never adds interests or the photo. **Never, at any tier:** age band,
cohort, progression, counts, attendance, activity history, last-seen.

Surfaces (all calling the same `profile_card` service — gates never in views):
- Web person page `/people/<public_id>/` + hover partial `/people/<public_id>/card/`
  (rate-limited `allow_action("profile_card")`, default 240/h) sharing `_person_card.html`.
- API `GET /api/connections/people/<public_id>/`.
- Hover overview: `static/js/hovercard.js` — one pair of document-level delegated
  listeners (site.js precedent), 300 ms hover intent, per-id cache, focus/Escape keyboard
  support, progressive enhancement over the plain person link; styling via `.hovercard`
  classes only (CSP has no style nonce). The hover card always shows the GENERATED avatar —
  uploaded photos remain a profile-page-only surface.
- **The image is a MUST:** activity roster rows, pending join requests, thread authors
  (activity + group), and group roster rows now carry the generated avatar +
  `data-hovercard-user` trigger, batch-loaded via `attach_interest_nodes` (constant
  queries, pinned by tests).

Tightening folded in (owner-approved): the DISPLAYED activity roster is now mutually
block-filtered via `social.services.visible_roster` (group_roster precedent). Display only —
membership checks, votes, and notification fan-outs keep using `current_members`.

### Post-implementation review outcomes (2 Opus lenses; all integrated)

- **One anti-scrape budget for all three card surfaces** (`web.views.profile_card_allowed`,
  shared `allow_action("profile_card")` bucket): the web page and the connections API are
  braked identically to the hover partial — a brake on one of three equivalent routes is no
  brake. (MED)
- **The stranger card never leaks the username handle**: a blank display name falls back to
  a neutral "A member" placeholder at STRANGER; the username fallback resumes at SHARED,
  where the handle is a permitted field. (MED)
- The join-request probe is computed once (`_resolve`) and threaded into the card; the
  shared-context overflow count comes from the service (no template arithmetic coupled to
  `_CONTEXT_LIMIT`); the person-page helptext names the join-request basis; the N+1 pin is a
  constant-DELTA test (query count equal at 2 and 7 members). (LOWs)
- Review verified clean: XSS (autoescape, no |safe), CSP (class-based styling only),
  minor clamp on every output path, photo defence-in-depth (`can_view_photo` at signing),
  shared-context intersection (nothing the viewer isn't in), guardian-seat exclusion,
  mutual block invisibility on every new surface, 404-collapse of all vetoes (an adult
  cannot confirm a minor's existence; public_ids are unguessable UUID4).

## Consequences

- A person's reachable audience is bounded by cohort + shared context; strangers see only
  the SAFETY.md-capped minimal card; blocks produce mutual 404s.
- SPA: person-page screen deferred (SSR is the primary surface; the SPA keeps deep-linking
  to the SSR page). A future `HoverCard.tsx` must ship as a lazy chunk (ADR-0022 budget has
  ~3.3 KiB headroom).
- New settings knobs: `PROFILE_CARD_RATE_LIMIT` / `PROFILE_CARD_RATE_WINDOW_SECONDS`
  (getattr defaults 240 / 3600).
