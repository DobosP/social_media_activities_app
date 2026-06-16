# Feature catalog — 2026-06 ideation, WAVE 4

> Produced by the feature-ideation-catalog workflow: map → ideate (per-theme lenses) →
> cluster/reject invariant-violators → adversarial evaluate (each candidate read against the
> live code at the named line) → synthesize. Built AFTER the original 2026-06 catalog, the
> WAVE-2 starter set, and the WAVE-3 wave (W3-F1/F16 shipped, the rest catalogued). These are
> NEW candidates that do not duplicate shipped behaviour; every seam was grepped and verified
> against `origin/main`. Verdicts: keep / revise (revise = ships only with the load-bearing
> reshape folded into its sketch). Effort S/M/L; impact 1-5; risk low/med/high. NOTE: WAVE-4
> ids (F1..F31) are a FRESH namespace — unrelated to the original catalog's OR WAVE-2's OR
> WAVE-3's F-numbers.

## Recommended starter set: F2, F5, F11, F12, F18, F22

A coherent, low-risk, high-leverage first batch that advances child-safety, organizer tooling,
reliability, discovery, place-data quality, and privacy in one wave — every pick is impact>=3,
keep-verdict or a clean revise, low risk, with no Phase-2 dependency, no legally-gated bet, and
nothing dark-until-a-flag. F2 (live supervisor legibility on the guardian manifest) is the
highest-leverage child-safety win: it replaces a static `Activity.supervised` chip that can
falsely reassure a parent with the honest live `supervision_satisfied` three-state, at the exact
manifest seam, with no model and no migration. F5 (reuse-a-meetup clone) is the supply-side
keystone: a prefill-only "set up another like this" that lets a volunteer coach who runs the same
session weekly stop re-typing the venue/logistics — re-validated through `create_activity`'s full
gate, so a clone can never escape the child-safety envelope. F11 (day-of meeting-point sanity
check) is a true S-effort reliability quick win: a passive inline banner that fires at the
quorum-reached moment a meetup becomes real but still has nowhere to gather — no job, no
notification, no new model. F12 (convene-around-this-event gauge) closes the find-and-go loop for
the browser who finds a real event but no meetup: a validated-GET prefill into the shipped F27
gauge, count-only, cohort-pinned. F18 (self-only my-venues data-quality digest) is a pure read
that flags venues with unverified hours / reported-closed / pending-correction for the meetups
the viewer is actually going to — a page, never a job. F22 (complete-the-export) is the flagship
privacy-dignity fix: the Art.20 download finally includes the user's own DSA safety record,
blocklist, and mute settings via the already-hardened self-scoped reads, with a schema bump. All
six are impact>=3 (one at impact 4), keep-or-clean-revise, and together they touch six of the
eight themes without taking on `med`/`high`-risk crowd-overlay or accessibility-claim work.

**Quick wins:** F2, F4, F5, F11, F12, F14, F18, F22, F24, F30, F31  ·  **Big bets:** none this wave (no L-effort candidate survived)

## Sequencing notes

Sequencing and dependency advice, grounded in the codebase:

1. WARDS-MANIFEST annotation cluster (F2, F4, F18-of-WAVE3-vintage). F1 (guardrail dry-run
   preview), F2 (live supervisor chip), and F4 (child-approved venue credit) all annotate the SAME
   `wards` view loop (apps/web/views.py:2748, `ward.meetups`, place already `select_related`) and
   render into the same `wards.html` rows. Ship them in one annotation pass to avoid three separate
   diffs over the identical loop. F1's count helper MUST reuse the SAME `_passes_guardrails`
   enforcement fn (apps/social/services.py:533), never a re-implementation, or the preview drifts
   from the gate and lies; F2's chip MUST call `supervision_satisfied` at RENDER time (never cache
   `a.supervised`) or it can't deliver its own docstring promise ("can never lie after the guardian
   leaves"); F4 must emit NO credit (silent omission) when the venue reads `unknown` at render time
   (a since-deactivated `ChildVenueClass`), never a false "approved" claim.

2. F1 COUNT-DENOMINATOR honesty (the inv.2 load-bearing edge). Do NOT count raw `can_join(ward, a)`
   pass/fail — `can_join` also returns False for already-joined (apps/social/services.py:506-510)
   and capacity-full (504) activities, neither of which is a guardrail block, so a raw count
   conflates "your limits block this" with "already joined / full" and the panel lies about WHY N is
   low. The helper must isolate the CHILD-specific gates (`_passes_guardrails` + `_venue_ok_for_child`)
   over OPEN+future `visible_activities(ward)`, exclude already-joined/full from the denominator, cap
   the slice (next ~50 by `starts_at`), and the copy must say exactly what is counted.

3. F3 GUARDIAN-MODERATION fan-out is offender+reporter, not offender-only. The sketch as first
   drafted only fans out for `target` and gates on `target.cohort==CHILD` — wrong: a content target
   (Post/Activity) is not a User. Resolve the affected user via `_affected_user(target)`
   (apps/safety/services.py:267) and gate on `affected_user.cohort==CHILD`; ALSO fire for a CHILD
   `report.reporter` in BOTH `take_action` AND `dismiss_report` (dismiss has no offender at all, so
   the reporter is the only minor there); dedup one-notice-per-guardian across the offender+reporter
   union within a single call. Body is a pure pointer to /wards/ — zero reason/identity detail.

4. ORGANIZER-EDIT-STATE discipline (F6 run-sheet, F7 bring-list, F9 co-organiser). The shipped
   `update_activity` family is the precedent: `@transaction.atomic` + `is_organizer` +
   `status==OPEN AND starts_at>now` + bounded length/count enforced IN THE SERVICE (not only the
   form) + `record_audit` inside the txn. F6's `set_agenda` must re-implement this whole discipline
   as a peer (not just borrow `is_organizer`) so a CANCELLED/COMPLETED activity's run-sheet freezes
   like every other organiser-curated field. F9's `accept_co_organizer` must NOT re-run
   `grant_co_organizer`'s body — that fn re-checks `activity.owner_id != owner.id`, but at accept
   time the actor is the MEMBER, so it would raise `NotAMember`; factor the role-flip + eligibility
   into a shared internal helper both the offer path (actor=owner) and accept path (actor=member)
   call, or the accept path is dead on arrival.

5. TRANSIENT-SIGNAL CLEAR-ON-EVERY-EXIT (F7 bring-list). `claimed_by` is a who-brought-what edge —
   it is the banned inv.2 reliability record unless nulled on EVERY membership-exit, not just
   voluntary `leave_activity`. `SET_NULL` on the FK only covers a User hard-delete (GDPR erase); a
   leave/removal/block does NOT delete the User. The service must explicitly null that member's
   claims in-txn on `leave_activity` (mirroring the 4 transient resets already at
   apps/social/services.py:1105-1108), on safety `take_action`/eviction, on block-vs-activity, and
   inside `cancel_activity`/`complete_activity` — ship the clear as one `_clear_bring_claims(member,
   activity)` helper called from every exit path.

6. CAPACITY-HONESTY firewall (F30 support-person seat). The companion is NOT capacity-counted:
   leave `participant_count`/`open_positions`/`can_join` (apps/social/services.py:485-504) COMPLETELY
   untouched. Folding a companion into `seats_remaining` would let an accessibility declaration
   silently consume a member seat, BLOCK other joiners (can_join:504), and break the
   `min_to_go<=capacity` rule — three harms at once. Surface companions ONLY as a separate
   organizer-only logistical line; never a member-visible count, never a discovery surface.

7. CROWD-OVERLAY INGEST-SAFETY (F14 stale-source, F15 moved-venue, F16 duplicate, F19 accessibility
   facts). The shipped pattern (F26 `ActivityEdgeVote`, F28 `OpenNowReport`, W3-F13 closure) is:
   live in your OWN table, never on Place, read-time decay, NO hard `UniqueConstraint` (uniqueness is
   TEMPORAL, enforced per reporter/pair/decay-window in the service so post-decay re-reporting +
   self-heal work). Three traps: (a) F14 CANNOT honestly claim to surface a vanished OSM record off
   `last_seen_at` alone — `ingest_places` is operator-run with NO absence sweep, so a vanished record
   and a never-re-ingested area are identical on the timestamp; it needs a per-ingest-run/area
   provenance marker (hence M, not the zero-write read the pitch implied). (b) F15's moderator APPLY
   path must NOT write `Event.place` on an ICAL/GOOGLE feed event — `upsert_event` puts `place` in
   `defaults`, so a re-pin is clobbered on the next sync (the W3-F16 false-promise class); gate the
   write to `source in {USER, MANUAL}` or keep it a moderator HINT only. (c) F19 (accessibility
   facts) must stay OSM-FIRST and FAIL-CLOSED on the positive direction — a crowd quorum may surface a
   caution where OSM is silent but must NEVER produce a `FACT_TRUE`/"match" badge from crowd votes
   alone (a false "step-free" badge routing a wheelchair user to an inaccessible venue is a real-world
   harm); only a real OSM `wheelchair=yes`/`hearing_loop=yes` tag may assert accessible.

8. SOFT-BADGE NEVER-HIDES + SAME-AXIS (F20 sensory/pace, F19 accessibility). The shipped
   `matches_access_preference` (apps/places/services.py:210) is the rule: the soft badge returns
   match/unknown only and NEVER excludes unknown-state activities/venues. F20 must NOT match against
   the existing venue-level `AccessPreference.prefers_quiet` (it stays stored-and-dead per the
   standing verdict — a venue axis, not an activity-character axis; cross-axis matching is a
   misleading claim); add a NEW same-axis stated preference and match it only against the new
   activity chips. F19 must wire the new ACCESS keys into the SURFACE users actually read
   (`accessibility_facts`/`matches_access_preference`/the /access/ sort), not a second mute facts
   list, or the inclusion value never lands.

9. SAVED-SEARCH SIGNATURE + XOR (F13 community→saved-search). `create_saved_search` takes `city`
   (str), NOT `area` (FK) — it resolves city→Area internally. AND the `savedsearch_type_xor_category`
   constraint requires exactly one axis: seed the community's NARROWEST axis (if `community.activity_type_id`
   is set, pass `activity_type` and `category=None`; only a category-tier rollup passes `category`).
   Always-seeding `category` would silently broaden a type-tier browse ("Football") into its whole
   category ("Sport") — breaking the "I browsed THIS and want THIS" loop the feature sells. F12's gauge
   prefill has the dual gotcha: a gauge has NO time field, only a `coarse_window` enum, so derive it
   from the event's `starts_at` in LOCALTIME (the W3-F12 UTC gotcha), never mirror F40's precise
   `starts_at` seeding.

10. PURGE-RECEIPT inv.2 landmine (F25). A `PurgeReceipt(user, category=arrival, lifetime_count, ...)`
    is, for the arrival category, a per-user cumulative count of how many meetups the user set an
    arrival ping at — exactly the banned per-user attendance/presence rollup, and exactly the standing
    presence record the arrival-ping model is built to never become. Plus `expire_arrivals` is one
    bulk `.update()` with no per-user loop, so a per-user-per-category receipt is NOT a cheap upsert.
    Ship ONLY an AGGREGATE, non-per-user "the last photo sweep ran on DATE" confirmation for the
    genuinely per-item photo-attachment purge, with NO cumulative count and NO arrival category — and
    note it overlaps W3-F16 heavily (hence impact 2, the high-risk verdict if built naively).

11. WARD-OBSERVATION SCOPING (F28). The audit row carries `actor=guardian` + `data.conversation_id`,
    NOT the ward. A naive conversation-scoped projection in a GROUP conversation with CHILD members
    from different families would surface observation triggered by ANOTHER child's guardian
    (cross-family leak) and mislabel it as observation "of you." Query
    `AuditLog.objects.filter(actor_ref__in=[THIS ward's ACTIVE guardians], event__in=[...])` (uses the
    indexed `actor_ref`, cheap — inv.6) then keep only rows whose `conversation_id` is one the ward was
    an active Participant of. Fix the event-name too: the enable event is `messaging.guardian_observing`,
    not `guardian_observer_added`.

12. RECIPIENT-REGISTER FAIL-CLOSED COVERAGE (F23). Every recipient row must DERIVE presence from a
    live config truth (`MEDIA_S3_REGION`, `get_identity_provider()`, `get_payment_provider().name`,
    `REDIS_URL`) so the panel can never publish a FALSE "we don't share with X" claim — the
    `retention_disclosure` honest-null discipline. The donations row must derive from
    `get_payment_provider().name` (the default DeepLinkProvider DOES route the donor off-platform —
    "no payment processor connected" is itself a latent false claim). A coverage TEST must assert every
    configured external integration maps to exactly one register row, or a future Stripe-prod flip
    silently under-discloses and the panel becomes a lie.

## Themes

- **Child safety & guardianship** (F1, F2, F3, F4) — Close legibility/asymmetry gaps in the
  already-built guardian, supervision, and moderation machinery: an honest dry-run of what a guardian's
  combined limits currently allow, a live (never-stale) supervisor chip, a symmetric DSA moderation
  pointer to the responsible adult, and a child-venue "why approved" credit — all read-only,
  ACTIVE-relationship-keyed, no new minor-onboarding dependency, no stored location, fail-closed.
- **Organizer & facilitator tooling** (F5, F6, F7, F8, F9) — Turn the meetup card into a structured
  "run sheet" for volunteer coaches and librarians: clone a past meetup's logistics, an ordered agenda,
  a bring-list members claim off, a passive readiness echo, and an accept-first co-organiser handoff —
  no vanity metrics, no behavioural tracking, no cross-activity history.
- **Reliability & showing up in person** (F10, F11, F12-reliability) — Stop a real group scattering at
  the door: a structural backup-organiser readiness fact, a quorum-moment meeting-point sanity check, and
  a fixed-prompt rendezvous micro-post — all derived live, no stored location, no presence log.
- **Discovery: closing the find-and-go loop** (F12, F13, F14-discovery) — Convert browse dead-ends:
  an event→gauge bridge for demand that hasn't found a meetup, a one-tap community→saved-search, and an
  area filter on the events list — all area-only, soonest-first, no popularity, no count.
- **Place & event data quality** (F14, F15, F16, F18) — Keep "we already know the places" true: an
  honest source-freshness signal, a crowd-suggested correct venue for a misplaced event, a crowd
  duplicate-venue flag feeding a staff merge, and a self-only "my upcoming venues need a check" digest —
  ingest-safe overlays, counts-only, self-healing.
- **Accessibility & inclusion** (F19, F20, F21, F30) — Serve disabled, neurodivergent, and
  multilingual members: crowd-correctable accessibility facts (OSM-first, fail-closed), a sensory/pace
  "what to expect" facet, a spoken-language facet + filter, and a non-counted support-person companion
  seat — every match a soft never-hiding badge, every facet an organizer-declared fact.
- **Privacy & data-dignity as product** (F22, F23, F25, F26, F28) — Make the platform's strongest
  differentiator felt: complete the Art.20 export, a derived recipient/sub-processor register, an
  aggregate purge confirmation, one-click profile minimisation, and a ward-side observation log — all
  self-scoped, derived from live truth, never widening exposure.
- **Civic impact, transparency & sustainability** (F24, F27, F31) — Prove and fund the mission
  honestly: a staff-authored civic-outcome narrative, a cost-anchor delivery example, and a partner's
  give-back venue note — all aggregate-only or staff-text, donor-FK-free, never an "X of Y" bar.

## Candidates

### F1 — Guardrail dry-run preview (what your limits currently allow)  `[S/imp3/low/revise]`
*Theme: Child safety & guardianship*

**Pitch.** When a guardian sets a family-calendar window / category envelope / supervised-only limit on
a CHILD ward (W3-F1/F2/F7), they get no honest read of what those limits actually do — so a too-tight
combination silently blocks every meetup and looks like a broken app. A read-only preview on /wards/ shows
"with these limits, your child could join N of the next M upcoming meetups in their cohort," with the calm
"combined limits currently block everything" state already computed in `guardianship_capabilities`
surfaced honestly.

**Why it fits the invariants.** inv.3: read-only, keyed on the ACTIVE `GuardianRelationship` the manifest
already loads; CHILD-scoped; never widens access (preview only). inv.2: a bounded N-of-M count of the
GUARDIAN's OWN ward's eligibility — NOT a per-user reliability/attendance/vanity metric and not a discovery
surface; same legibility category as the shipped `guardrail_combined_blocks_all` (apps/accounts/services.py:952),
derived live, nothing stored. inv.4: no location, no DOB (meetups are already cohort-visible). inv.6: pure
Postgres read reusing the existing gate fns, bounded slice, no ML. Sharp edge: must reuse the SAME
`_passes_guardrails` decision fn enforcement uses (not a re-implementation), or the preview drifts from the
gate and lies; bound the candidate set to `visible_activities(ward)` so it never leaks an out-of-cohort
meetup's existence.

**Sketch.** Add a read helper `guardrail_preview(ward) -> {eligible, total}`: iterate OPEN+future
`social.visible_activities(ward)`, counting pass vs total with the CHILD-specific gates
(`_passes_guardrails(ward, a) and _venue_ok_for_child(a)`), excluding already-joined/full from the
denominator. Cap the slice (next ~50 by `starts_at`) so it stays bounded. Surface on the /wards/ panel
(apps/web/views.py:2734 `wards`, alongside `ward.caps = guardianship_capabilities`) and render in
wards.html under each ward's limits block. No new model, no migration. LOAD-BEARING RESHAPE: do NOT count
raw `can_join` pass/fail — it returns False for already-joined (apps/social/services.py:506-510) and
capacity-full (504) activities, neither a guardrail block, so a raw count conflates "your limits block
this" with "already joined / full" and the panel lies about WHY N is low (the pitch sells it as diagnosing
an over-tight guardrail). Isolate the guardrail decision via the same `_passes_guardrails` fn enforcement
uses, exclude joined/full from the denominator, and the copy must say exactly what is counted, matching
`combined_blocks_all`'s honesty bar.

**Depends on:** W3-F1/F2/F7 guardrails (`effective_guardrail`, `_passes_guardrails`, `guardianship_capabilities`);
`social.can_join` / `visible_activities`
**Touches:** apps/accounts/services.py (or apps/social/services.py for the helper); apps/web/views.py; apps/web/templates/web/wards.html

### F2 — Live supervisor legibility on the guardian manifest  `[S/imp4/low/keep]`
*Theme: Child safety & guardianship*

**Pitch.** A CHILD's supervised meetup shows the guardian a static "guardian-supervised" chip on /wards/
even when no supervisor is actually seated yet (the chip reads `Activity.supervised`, not whether a
supervisor is present right now). Replace the static flag with the honest live state —
"supervised (a guardian seat is filled)" vs "supervised — no adult seated yet" — so a parent isn't falsely
reassured.

**Why it fits the invariants.** inv.3: strengthens the core child-safety promise by making the supervision
fact non-misleading; read-only, ACTIVE-relationship-keyed via the existing manifest query; the live
predicate keys on `is_guardian_of(m.user, OWNER)` + GUARDIAN-role memberships, never "any participant," so
no adult→other-minor read-window. inv.2: a three-state boolean, no metric. inv.4: no location, no new PII.
inv.6: pure read reusing the existing live predicate, Postgres-only, no migration. Not minor-onboarding-gated
— `set_activity_supervision` is a CHILD-cohort guard, not behind `ALLOW_MINOR_ONBOARDING`, so supervised
activities are real today. Sharp edge: must call `social.supervision_satisfied(a)` at RENDER time (NOT cache
`a.supervised`) so a supervisor leaving mid-flow flips the chip — the whole point is it "can never lie after
the guardian leaves" (the docstring's own promise).

**Sketch.** In apps/web/views.py `wards` (line 2734), annotate each `ward.meetups` entry with
`a.supervision_live = social.supervision_satisfied(a)` (apps/social/services.py:1335 — already a single live
predicate over GUARDIAN-role memberships). In wards.html line 18, replace the
`{% if a.supervised %}guardian-supervised{% endif %}` with three-state copy driven by `a.supervised` +
`a.supervision_live`. No model/migration; the F29 supervision machinery already exists. LOAD-BEARING
RESHAPE: drop the optional "surface the seated supervisor's display name" sub-part — the load-bearing fix is
the boolean three-state chip (supervised+seated / supervised+no-adult-seated-yet / not-supervised) annotated
at render time; the name adds PII surface for no honesty gain. Keep the chip boolean-only.

**Depends on:** F29 supervisor seat (`supervision_satisfied`/`active_supervisor_present`); F6/F18 wards manifest
**Touches:** apps/web/views.py (wards); apps/web/templates/web/wards.html

### F3 — Guardian moderation-outcome notice (symmetric DSA loop for a minor)  `[S/imp3/low/revise]`
*Theme: Child safety & guardianship*

**Pitch.** When a moderator actions or dismisses a report and the offender or reporter is a CHILD,
`take_action` notifies only the minor themselves (Art.17 statement of reasons) and the reporter (Art.16) —
the ACTIVE guardian, legally responsible for the under-16, learns nothing. A non-mutable SYSTEM notice tells
each active guardian, in fixed server-composed copy, that a moderation outcome concerning their ward
occurred and points them at their guardian page — no detail leak, no moderator identity.

**Why it fits the invariants.** inv.3: fan-out keyed strictly on ACTIVE `GuardianRelationship` (mirrors
`_alert_guardians_unsafe` at apps/safety/services.py:140), excludes blocked pairs, at-most-one-per-guardian;
never adult→minor contact (it's a system notice). inv.2: SYSTEM kind, non-mutable but also non-baiting — one
notice per outcome. inv.4: carries NO report detail, NO other user's data, NO moderator identity — just "a
moderation outcome concerning your ward occurred; see your guardian page," field-allowlisted like
`file_unsafe_report`'s fixed copy. inv.6: one `notify()` call, Postgres-only. Sharp edge: must NOT reveal
what the ward did or who reported (DSA Art.17 detail belongs only to the offender) — the guardian notice is a
pure pointer.

**Sketch.** In `take_action` (apps/safety/services.py:523) and `dismiss_report`, after the existing
`_notify_statement_of_reasons`/`_notify_reporter` calls, add `_alert_guardians_of_moderation(affected_user)`,
modelled on `_alert_guardians_unsafe` (line 140): loop ACTIVE `GuardianRelationship`, skip `blocked_user_ids`
pairs, send `notify(guardian, Kind.SYSTEM, fixed_title, body, url='/wards/')`. SYSTEM is already non-mutable
(`NON_MUTABLE_KINDS`). No new Kind, no migration. LOAD-BEARING RESHAPE: the sketch's offender-only,
`target.cohort==CHILD` gate is wrong for content targets and drops the reporter side the pitch promises. Ship
with: (1) resolve the affected user via `_affected_user(target)` (a Post/Activity target is not a User) and
gate on `affected_user.cohort==CHILD`, not `target.cohort`; (2) ALSO fire for a CHILD `report.reporter` in
BOTH `take_action` AND `dismiss_report` (dismiss has no offender at all, so the reporter is the only minor
there); (3) one-notice-per-guardian dedup must span the offender+reporter union within a single call so a
guardian of both never gets two notices for one outcome; (4) body stays a pure pointer to /wards/ with zero
reason/identity detail, savepoint-isolated per the F8 template.

**Depends on:** the unsafe-button guardian-alert pattern (`_alert_guardians_unsafe`); `notifications.notify`;
`blocked_user_ids`; `GuardianRelationship`; `_affected_user`
**Touches:** apps/safety/services.py (take_action, dismiss_report, new `_alert_guardians_of_moderation`); apps/safety/tests

### F4 — Why-this-venue-is-child-approved note on the guardian manifest  `[S/imp2/low/keep]`
*Theme: Child safety & guardianship*

**Pitch.** The /wards/ manifest shows a child's meetup place by name, but a parent can't see WHY that venue
is permitted for a child meetup — only staff see the `ApprovedChildVenue` override / `ChildVenueClass` tag
match. A one-line read-only "child-approved: public library (staff-verified)" / "child-approved: matches
public-venue rule" credit on each manifest row makes the F9 venue gate legible to the responsible adult.

**Why it fits the invariants.** inv.3: surfaces the existing fail-closed child-venue decision
(`public_child_venue_class`) — strengthens the public-venue-only promise by making it auditable to the
guardian; read-only, ACTIVE-relationship-scoped (only on the guardian's own wards' meetups, which already
passed the gate). inv.2: a static honest credit, no metric. inv.4: venue type only, no location storage, no
PII. inv.6: pure read reusing `public_child_venue_class` (apps/places/services.py:167), a tiny staff table,
no migration. Sharp edge: only ever emit the "allowed" rationale; deliberately do NOT render an
"unknown/not-allowed" state on the manifest, and never expose raw OSM tags — only a humanised "staff-verified"
vs "matches public-venue rule" label so it can't become a tag-scraping oracle.

**Sketch.** Add a small read helper `places.child_venue_rationale(place) -> short label`, derived from
`public_child_venue_class` + whether an `ApprovedChildVenue` row exists vs a `ChildVenueClass` tag match
(apps/places/services.py:167-207). In `wards` (apps/web/views.py:2734), annotate each `ward.meetups` entry
(place already `select_related`). Render in wards.html near the existing place line. No model, no migration.
Composes with F2 (same manifest annotation pass). IMPLEMENTATION GUARD (load-bearing): the manifest filters
meetups by cohort only, NOT by re-checking `is_child_safe_venue`, so an existing OPEN child meetup whose
`ChildVenueClass` was later deactivated will read "unknown" at render time; the helper must then emit NO
credit (silent safe omission), never a false "approved" claim.

**Depends on:** F9 child-venue gate (`public_child_venue_class`/`is_child_safe_venue`); F6/F18 wards manifest
**Touches:** apps/places/services.py (`child_venue_rationale` helper); apps/web/views.py (wards); apps/web/templates/web/wards.html

### F5 — Reuse-a-meetup: clone a past activity into a new one (prefill-only)  `[S/imp3/low/keep]`
*Theme: Organizer & facilitator tooling*

**Pitch.** A volunteer coach who runs the same Tuesday football session shouldn't re-type the venue,
what-to-bring, cost band, and meeting point every week. This adds a "set up another like this" link on a
COMPLETED/cancelled activity the user organised, seeding the create form from the old activity's fields —
exactly like the existing event-prefill (F40), but sourced from a meetup the organiser already ran.

**Why it fits the invariants.** inv.2: pure form prefill, no new state, no per-user history written, no
count surfaced; the source is the organiser's OWN activity. inv.3/5: the seeded place/type/cohort are
re-validated through `create_activity`'s full gate — `public_places()`, `is_child_safe_venue` for CHILD,
`category_envelope_allows`, `can_create_activity`; cohort is pinned from the owner, never copied from source
— so a clone can never escape the child-safety envelope; the prefill only fills fields the organiser still
confirms. inv.4: activity fields only, no coords stored. inv.6: template/dict prefill, no migration. Sharp
edge: must validate every GET value server-side before seeding (mirror F40's `activity_create` GET
validation) so a tampered `?from=` can't inject a place/type the user couldn't otherwise pick.

**Sketch.** Add a `draft_from_activity(user, source_activity) -> dict` helper in apps/social/services.py
beside `draft_activity_text` (line 2574) that, after asserting
`source_activity.owner_id == user.id OR is_organizer(user, source_activity)`, returns the whitelisted prefill
dict (title/description/meeting_point/what_to_bring/organizer_note/cost_band/difficulty/accessibility_notes/
beginners_welcome/capacity/min_to_go — NEVER `starts_at`, NEVER the old membership). Wire it into the existing
`activity_create` web view (apps/web/views.py:1529, the real F40/F36 GET-seeding view) which already seeds via
`setdefault`, and add a "Set up another like this" link on the COMPLETED `activity_detail` template. No model
change, no migration.

**Depends on:** F40 organize-here prefill (`activity_create` GET-seeding via `setdefault`) + `draft_activity_text`;
reuses `create_activity`'s gate + `is_organizer`
**Touches:** apps/social/services.py; apps/web/views.py; apps/web/templates/web/activity_detail.html; apps/social/tests/test_draft.py

### F6 — Run-sheet: owner-curated ordered agenda lines on an activity  `[M/imp3/low/revise]`
*Theme: Organizer & facilitator tooling*

**Pitch.** Volunteer librarians and coaches plan a session in steps ("15:00 warm-up, 15:20 drills, 15:50
cool-down"). Today the only structured fields are meeting_point/what_to_bring/organizer_note free text. This
adds a small ordered list of agenda lines, members-only, so a facilitator can publish the plan and a stand-in
co-organiser can run it.

**Why it fits the invariants.** inv.1: text-only, no media. inv.2: no metrics, no tracking — static
owner-authored content. inv.3: `agenda_for` gates on `can_read_thread`, which enforces the SINGLE fail-closed
cohort wall (`user.cohort==activity.cohort`) + `can_participate` + thread membership + block check; a CHILD
run-sheet is visible only to cohort-isolated members. (Note: `can_read_thread` does NOT include supervisory
guardians — the guardian sees the meetup via the /wards/ manifest path, which is more conservative, not a
leak.) inv.4/5/6: no PII/location, reinforces in-person structure, Postgres-only, no ML. Sharp edge:
editability-state divergence from the whitelisted-field discipline.

**Sketch.** New `ActivityAgendaLine(activity FK, position SmallInt, text CharField(max 200))` with
`unique_together(activity, position)`; service fns `set_agenda(owner, activity, lines)` / `agenda_for(activity,
viewer)` beside the logistics-card editing. Web edit on the existing logistics edit screen; read on
activity_detail. One migration. LOAD-BEARING RESHAPE: `set_agenda` must NOT just borrow `is_organizer` — it
must re-implement `update_activity`'s full edit-state discipline as a peer: `@transaction.atomic`,
`is_organizer`, `status==OPEN AND starts_at>now` guards (so a CANCELLED/COMPLETED run-sheet freezes like every
other organiser-curated field), bounded line count (<=20) and per-line length (200) enforced IN THE SERVICE
(not only the form), and an explicit `record_audit` inside the txn (match the `invoke_fallback` audited-edit
pattern at services.py:1275). `agenda_for` must reuse the caller's `is_member` signal exactly like
`plain_meetup_brief` (services.py:2618-2649) and emit nothing to non-members. Without these the run-sheet
becomes an editable-anytime side channel that diverges from the discipline the pitch claims to inherit.

**Depends on:** the logistics-card edit path (`is_organizer`, `update_activity`); `can_read_thread`; composes with co-organizer (`grant_co_organizer`)
**Touches:** apps/social/models.py; apps/social/migrations/; apps/social/services.py; apps/web/views.py; apps/web/forms.py; apps/web/templates/web/activity_detail.html; apps/social/tests/test_agenda.py

### F7 — Bring-list sign-up: members claim items off the what-to-bring list  `[S/imp3/low/revise]`
*Theme: Organizer & facilitator tooling*

**Pitch.** When a coach posts "bring: 2 footballs, a first-aid kit, cones," members currently can't
coordinate who brings what — they double up or nobody brings the kit. This lets the organiser publish a small
list of needed items and members claim one each (claim/unclaim), shown as a members-only checklist. No
who-brought-what history is ever kept past the meetup.

**Why it fits the invariants.** inv.2: NO per-user reliability/"who reliably brings things" rollup — claims
are transient and cleared on leave (mirroring `attendance_intent`/`met_confirmed` clearing). No counts on any
discovery surface; the checklist is members-only. inv.3: claims gated by current membership + `can_participate`;
a CHILD activity's list is cohort-isolated; the claimer is a co-present peer, not new contact. inv.4: no
location/PII added. inv.5: directly serves in-person logistics. Sharp edge: a claim is a transient
coordination signal, NOT an attendance record — it must clear on EVERY exit and never aggregate cross-activity.

**Sketch.** New `BringItem(activity FK, position, text)` + nullable `claimed_by` FK to User (SET_NULL).
Services `set_bring_items(owner, activity, items)` (organiser, F2-style), `claim_item`/`unclaim_item` gated by
`current_members(activity).filter(user=member)` + `can_participate` (the same union gate `post_to_thread`
uses). Members-only read via `can_read_thread`. One migration. LOAD-BEARING RESHAPE: `claimed_by` must be
nulled on ALL membership-exit + activity-end transitions, not only `leave_activity`: also on safety
`take_action`/eviction, on block-vs-activity, and inside `cancel_activity`/`complete_activity` (claims are a
live-coordination signal, meaningless once the meetup is over). `SET_NULL` only covers a User hard-delete; a
leave/removal/block does NOT delete the User, so the service must explicitly null that member's claims in-txn
on every such path. Ship the clear as a single `_clear_bring_claims(member, activity)` helper called from
every exit path, mirroring how `leave_activity` (services.py:1105-1108) batches its resets. Otherwise a stale
"Ana → first-aid kit" persists after Ana is removed and becomes exactly the lingering who-did-what record
inv.2 forbids.

**Depends on:** the `what_to_bring` logistics field; `current_members`/`can_participate` gate; the leave-activity transient-clear pattern
**Touches:** apps/social/models.py; apps/social/migrations/; apps/social/services.py; apps/web/views.py; apps/web/templates/web/activity_detail.html; apps/social/tests/test_bring_list.py

### F8 — First-time-organiser readiness echo on the console (passive, deterministic)  `[S/imp2/low/revise]`
*Theme: Organizer & facilitator tooling*

**Pitch.** A volunteer librarian creating their first activity has no idea what makes a meetup go well here
(set a meeting point, post an announcement before start, mark complete after). This renders the organizer
console's already-computed per-activity readiness as plain human-readable "still to do" lines that link into
the existing edit screens, plus one small static cohort-aware "running a good meetup" note.

**Why it fits the invariants.** inv.2: a passive on-page panel only — NO nudges, NO notifications, NO
re-pestering (the active nudge lane is already covered by W3-F6 ORGANIZER_PREP). The readiness items are
per-activity TASK snapshots reused from `organizer_console` (services.py:395-414, explicitly "a gap to fix,
never a per-organizer score"), so no rollup/leaderboard. inv.6: 100% deterministic template content, zero ML,
zero per-user AI spend. inv.3: the guide text is cohort-aware (CHILD organisers see the supervisor-seat +
public-venue lines), reading already-isolated console data. Sharp edge: it must not become a notification or
an engagement loop — strictly a static page section.

**Sketch.** Render `organizer_console`'s already-computed per-row `readiness`/`missing_meeting_point`/
`venue_flag`/`needs_supervisor`/`quorum` sub-dicts as plain "still to do: set a meeting point → [edit]" lines
on the existing console page (apps/social/services.py:303 → apps/web/views.py:2256 → organize.html), plus one
small static cohort-aware note. No new service function, no new queries, no model/migration/notification kind.
LOAD-BEARING RESHAPE: DROP the originally-pitched static create-form handbook block — the create form is
already guidance-dense (per-field `help_text` in apps/web/forms.py + the F36 draft), so a second static block
is near-redundant noise. Ship ONLY the console-side presentation-layer change over data the service already
returns. (Fix the sketch's seam error: apps/web/views.py:2256 is the console, NOT the create form — the create
form is `activity_create` at apps/web/views.py:1529.)

**Depends on:** `organizer_console` (W2-F5/W3-F5) sub-dicts; the organize console view; complements W3-F6 ORGANIZER_PREP (no overlap — this is passive)
**Touches:** apps/web/views.py; apps/web/templates/web/organize.html; apps/social/tests/test_organizer_console.py

### F9 — Co-organiser invite-by-request: an accept-first handoff of the co-organiser seat  `[S/imp2/low/revise]`
*Theme: Organizer & facilitator tooling*

**Pitch.** Today `grant_co_organizer` is a unilateral owner action — the owner adds a member with no consent
from that person. For volunteer-run groups a facilitator should be able to *offer* a co-organiser seat and the
member accept it, so nobody is silently saddled with running someone else's meetup. This makes the existing
co-organiser seat mutual opt-in.

**Why it fits the invariants.** inv.2: accept-first, no pestering — one offer, idempotent, no re-notify
(mirror `request_connection`'s idempotent+rate-limited design in connections/services.py:156). inv.3:
co-organiser handoff is already structurally adult-only (`grant_co_organizer` asserts `cohort==ADULT` at
services.py:1549, pinned by `test_minor_cohort_has_no_co_organizer_or_transfer_path`) — both offer and accept
must route the role flip through that same assertion + `_coorg_eligible`, so the minor guarantee is preserved;
no adult↔minor organiser path. inv.4: one nullable timestamp or a tiny offer row, no new PII. An
unaccepted/stale offer grants zero authority (re-gate at accept catches a left-membership or cohort change).

**Sketch.** Extend Membership with a nullable `co_organizer_offered_at` (or a tiny `CoOrganizerOffer` row
keyed on activity+member). Split `grant_co_organizer` (services.py:1533) into `offer_co_organizer(owner,
activity, member)` → ORGANIZER_ROLE notification (existing mutable kind) and `accept_co_organizer(member,
activity)`. Keep `revoke_co_organizer`/`transfer_ownership` unchanged. Web accept button on activity_detail;
DRF action on the activity viewset. One migration. LOAD-BEARING RESHAPE: `accept_co_organizer` must NOT
"re-run `grant_co_organizer`'s body" — that fn re-checks `activity.owner_id != owner.id` (services.py:1547),
but at accept time the actor is the MEMBER, so it would raise `NotAMember`. Factor the role-flip + eligibility
checks (owner-still-owns the OFFER + `cohort==ADULT` + `_coorg_eligible` + offer still pending) into a shared
internal helper that both `accept_co_organizer` (actor=member) and the offer path call, so the owner-actor gate
isn't wrongly applied to the acceptor. Without this split the accept path is dead on arrival.

**Depends on:** the co-organizer seat (`grant_co_organizer`/`transfer_ownership`); ORGANIZER_ROLE notification kind; the accept-first/idempotent pattern from `connections.request_connection`
**Touches:** apps/social/models.py; apps/social/migrations/; apps/social/services.py; apps/social/views.py; apps/web/views.py; apps/web/templates/web/activity_detail.html; apps/social/tests/test_co_organizer.py

### F10 — Backup organiser readiness flag (showing-up resilience)  `[S/imp2/low/revise]`
*Theme: Reliability & showing up in person*

**Pitch.** On the organiser prep console, surface a calm one-line readiness fact: "No co-organiser yet — if
you can't make it, the meetup has no backup." for upcoming ADULT meetups with an unfilled co-org seat.
Reliability often fails because the single organiser drops out; this nudges (read-only, in the console they
already open) toward seating the co-org that already exists in the model.

**Why it fits the invariants.** inv.2: NO per-user reliability history — this is a derived live fact about
the MEETUP's structure (has a co-org or not), never about whether the organiser has flaked before; no count,
no score; same shape as the existing readiness booleans (`missing_meeting_point`, `near_capacity`). inv.3:
co-org grant already re-runs cohort/eligibility gates via `_coorg_eligible`; this only describes, changes no
gate. inv.4: no PII, no location. inv.5: keeps real meetups from collapsing at the door. inv.6: pure read
aggregation, no new model. Sharp edge: it must be a description-only fact in an existing self-opened surface,
NOT a notification/nudge that re-pings; resist ranking organisers by past reliability.

**Sketch.** Extend `organizer_console` (apps/social/services.py:303) — it already builds a per-activity
readiness dict. Add a derived `has_backup_organiser` boolean per row by checking for a second MEMBER with the
co-organizer role. Render the calm line in the existing console template. No new field, no job, no
notification. LOAD-BEARING RESHAPE: gate `has_backup_organiser` to ADULT activities only — `grant_co_organizer`
raises `InvalidState` for any non-ADULT activity (services.py:1549), so the seat doesn't exist on CHILD/TEEN
meetups; the row sets `has_backup_organiser` (and the "no backup if you drop out" line renders) only when
`a.cohort==Cohort.ADULT`, else it nudges a child organiser toward an action the model refuses. Compute it as a
batched annotate on the existing console queryset (`Count` of memberships with `role=CO_ORGANIZER, state=MEMBER,
distinct=True`) so it stays O(1) queries — never a per-row `voting_members()` call inside the comprehension.
Keep it a plain task-gap boolean (no count surfaced); the copy describes meetup structure, never the
organiser's history.

**Depends on:** the organizer prep console (W3-F5); the co-organizer seat
**Touches:** apps/social/services.py (organizer_console); apps/web/templates/web/organize.html; apps/social/tests/

### F11 — Day-of meeting-point sanity check before quorum-go locks  `[S/imp3/low/keep]`
*Theme: Reliability & showing up in person*

**Pitch.** When a meetup reaches its minimum-to-go quorum (the moment it's actually happening) but still has
a blank meeting point, show the organiser a one-time inline "this is now going ahead — add where to meet so
people can find you" prompt on the activity page they're already on. It catches the highest-stakes reliability
gap (a confirmed meetup nobody can locate) at exactly the moment it becomes real, without adding another
scheduled nudge job.

**Why it fits the invariants.** inv.2: not a notification/re-ping — a passive inline banner on a page the
organiser already visits, derived from values already computed in the view (`is_organizer` at views.py:1360,
`rsvp_summary.met_minimum` at views.py:1499), no count, no history, self-suppressing the instant
`meeting_point` is non-blank. inv.3/4: no PII, no location, cohort-neutral. inv.5: protects the in-person
show-up of an already-committed group. inv.6: derived live from existing fields (`min_to_go`,
`meeting_point`) + the already-derived live quorum count — no model, no DUE_JOB, no Notification.Kind,
Postgres-only. Sharp edge: must reuse the existing live quorum count, not invent per-view state, and
self-suppress once `meeting_point` is filled. Distinct from W3-F6 (the 48h-window console flag + nudge job) —
different trigger, different surface, complementary.

**Sketch.** Add a read-only helper `quorum_locked_without_meeting_point(activity)` in apps/social/services.py
returning True when `min_to_go is not None and going_count >= min_to_go and not meeting_point.strip()`,
reusing the quorum count logic already in `attendance_summary` (apps/social/services.py:2472, the live
`met_minimum`). Call it in the web `activity_detail` view (apps/web/views.py) for the owner/co-org and render
a calm inline banner near the existing "It's on" tag (activity_detail.html:193) and meeting_point block
(:296). No DUE_JOBS entry, no Notification.Kind.

**Depends on:** quorum-go (`min_to_go`/live `met_minimum`); the logistics-card `meeting_point`
**Touches:** apps/social/services.py; apps/web/views.py; apps/web/templates/web/activity_detail.html; apps/social/tests/

### F12 — Convene-around-this-event interest gauge (event → proto-meetup bridge)  `[S/imp3/low/keep]`
*Theme: Discovery: closing the find-and-go loop*

**Pitch.** A browser who finds a real event they'd attend but where no meetup has formed yet currently hits a
dead end — the only affordance is "organise it yourself" (F40), which most newcomers won't do. This adds a
calm "I'd go if someone convened" link on an event that seeds the existing F27 interest-gauge, letting
low-commitment demand surface and a proposer step in — closing the loop from "found a thing" to "a group is
forming."

**Why it fits the invariants.** Reuses the shipped F27 ephemeral gauge — count-only, no per-user history,
ephemeral/expiring, prefill is GET-only seeding the proposer edits (inv.2). inv.3: cohort pinned from the
PROPOSER in `propose_interest`, not the event; CHILD child-venue + W3-F2 category-envelope gates already fire;
the gauge M2M is never a Membership so it can never feed `connections.can_connect` (pinned by an existing
test). inv.4: no coordinate stored — the event already carries its place FK. inv.5: the gauge is bound to the
event's real public place + active activity_type, never placeless. Sharp edge: the event's place may be a
still-PENDING user-proposed venue — `event_detail` already routes through `events_with_public_places()` and
`GaugeForm.place`/`propose_interest` both re-narrow to `public_places()`, so a pending F25 place can never seed
a gauge.

**Sketch.** Mirror the F40 validated-GET prefill (apps/web/views.py:3513 `gauge_create`, today an empty
`GaugeForm()` else-branch) but target the gauge-create form: validate `event_id` via
`events_with_public_places()`, then seed the form's `activity_type` + `place` from the event. The gauge itself
is the existing `ActivityInterest` model created through the existing `propose_interest` service
(apps/social/services.py:3403, already gates `public_places` + child-venue + category-envelope) — no new
model. Add a "convene from event" link on event_detail.html (line 36, beside the F40 activity link) guarded by
`can_participate`. LOAD-BEARING RESHAPE: a gauge has NO time field — only a `coarse_window` enum
(weekday/weekend × daytime/evening). Do NOT mirror F40's precise `starts_at` seeding. Derive `coarse_window`
from the event's `starts_at` in LOCALTIME (the W3-F12 UTC gotcha) as a prefilled-but-editable default, OR seed
only place+type and leave the window unselected. Do NOT add a time field or a new service param — the existing
`propose_interest(place, activity_type, coarse_window)` signature suffices, so the candidate's optional
"event-source helper" is dead weight; keep it purely web-layer prefill.

**Depends on:** F27 ephemeral gauge (`propose_interest`); F40 organize-one-here prefill; F25 public-place gate (`events_with_public_places`)
**Touches:** apps/web/views.py; apps/web/templates/web/event_detail.html; apps/web/urls.py; apps/web/forms.py (GaugeForm prefill)

### F13 — Save-this-search from a community page (community → saved-search one-tap)  `[S/imp3/low/revise]`
*Theme: Discovery: closing the find-and-go loop*

**Pitch.** Communities (e.g. "Cluj-Napoca Football") are derived geo×type discovery labels but a dead-end
browse: you can list a community's activities, but if none fit right now you leave and forget. This adds a
one-tap "Tell me when a new one is listed" that materialises a SavedSearch pre-filled from the community's
exact geo (Area) + taxonomy (type/category) pins — closing the loop from "browsed a community" to "the
one-notice-ever F3 alert" without re-typing a filter.

**Why it fits the invariants.** inv.2: reuses the F3 SavedSearch one-notice-ever, opt-in, area-only alert
(no pestering, no re-notify, hard-capped, dedup'd, audited) — no engagement loop added. inv.3/inv.4:
`community_by_slug` routes through `visible_communities(viewer)` so the slug is cohort-walled (cross-cohort =
404); `create_saved_search` pins `cohort=user.cohort` and the matcher re-asserts viewer==saver cohort, so the
prefill can't cross the cohort wall even if mis-seeded; geo is AREA/city-only (`Community.area`→Area.city),
never a coordinate. inv.6: zero new model, zero migration — maps Community's two existing FK pins onto
SavedSearch's existing fields. Sharp edge: the SavedSearch type-XOR-category constraint must be honoured.

**Sketch.** `community_detail` (apps/web/views.py:158) reads `community_by_slug`
(apps/communities/services.py:74, cohort-asserting). Add a POST handler that calls `create_saved_search`
(apps/saved_searches/services.py:86). A single button on community_detail.html. The existing
`match_saved_searches` then handles the alert with no change. No migration. LOAD-BEARING RESHAPE: two
corrections. (1) SIGNATURE — `create_saved_search` takes `city` (str), NOT `area` (FK); it resolves city→Area
internally via `_ensure_city_area` after its anti-abuse gates. Pass `city=community.area.city`, not
`area=community.area`. (2) AXIS FIDELITY + XOR — `Community.category` is ALWAYS set and `Community.activity_type`
is set only for type-tier; the `savedsearch_type_xor_category` constraint requires exactly one. Seed the
community's NARROWEST axis: if `community.activity_type_id` is set (type-tier, e.g. "Cluj-Napoca Football")
pass `activity_type` and `category=None`; only a category-tier rollup passes `category`. Always-seeding
`category` would silently broaden a type-tier browse ("Football") into its whole category ("Sport"), breaking
the "I browsed THIS and want THIS" loop the feature sells.

**Depends on:** F3 saved-search alerts (`SavedSearch`/`create_saved_search`/`match_saved_searches`); Communities (`community_by_slug`, `Community.area`/`category` pins)
**Touches:** apps/web/views.py; apps/web/templates/web/community_detail.html; apps/web/urls.py; apps/saved_searches/tests/

### F14 — Area-scoped "what's happening near me" events parity filter  `[S/imp3/low/revise]`
*Theme: Discovery: closing the find-and-go loop*

**Pitch.** Activities get request-only proximity ranking (F5) and an area-aware discovery surface, but the
public events browse (/events/) only offers a type filter and a flat soonest-first list — a weaker half of the
find-and-go loop, since events are often the SEED a meetup forms around. This adds an Area filter to the web
events list, so a browser can narrow "what's happening" to their part of the city before deciding to convene.

**Why it fits the invariants.** inv.4: the Area filter is `_area_place_q = Q(place__address_city__iexact=
area.city)` — no stored coordinate, identical to the shipped SavedSearch/Group/Community Area pattern ("the
ONLY geo scope — a city Area, never a coordinate"). inv.2: `events_list` keeps `order_by("starts_at")`
soonest-first; no count/popularity. inv.3: events are not cohort-isolated activities (no membership/private
contact), so an Area narrowing introduces no cross-cohort path. F25 child-safety/place gate: the filter
narrows `upcoming_events()` which already wraps `events_with_public_places()` → `public_places()`, so it cannot
surface an event at an unpublished proposed venue; a null-place event is correctly dropped once an area is
selected (no NULL-IN leak).

**Sketch.** `events_list` (apps/web/views.py:2577, the `upcoming_events().order_by("starts_at")` branch)
currently does an optional activity_type filter. Add an optional `?area=` that resolves to `communities.Area`
and applies `communities.services._area_place_q` (apps/communities/services.py:33) to the gated queryset;
mirror the area dropdown the activities list/saved-search UI already uses. Keep the "Filtered by X" honesty
banner so area composes visibly with q/activity (mirrors the existing review-W1-28 note already in
`events_list`). LOAD-BEARING RESHAPE: ship Area-only. DROP the originally-bundled "optionally surface
request-only proximity" sub-part — it is the larger, riskier half (template coord-parsing UI + Distance
annotation + distance-vs-soonest ordering), it duplicates the W1-F5 request-only proximity already on the
activities feed and the discovery `HappeningView`, and the candidate itself hedges it as "optional." The
load-bearing, novel, low-risk piece is the single `?area=` dropdown narrowing the already-F25-gated
`upcoming_events()` via `_area_place_q`.

**Depends on:** Event iCal feeds + `upcoming_events`/`events_with_public_places` (F25 gate); `communities._area_place_q` (Area)
**Touches:** apps/web/views.py; apps/web/templates/web/events.html; apps/events/services.py (optional thin area-filter helper); apps/web/tests/

### F15 — Crowd-suggest the correct venue for a misplaced event  `[M/imp3/med/revise]`
*Theme: Place & event data quality*

**Pitch.** Today an event can be flagged MOVED/"wrong place" (`EventReport`) but the report dead-ends at a
moderator with no proposed fix. This lets the same verified members who flag a moved event also point at the
already-known correct Place from our gazetteer, so moderators get a concrete, quorum-backed re-pin suggestion
instead of a bare complaint.

**Why it fits the invariants.** inv.6: the suggestion-COLLECTION half is a clean clone of the F21 EventReport
overlay — `EventReport.suggested_place` is an overlay FK (re-ingest never touches EventReport), FK-not-free-text
(no free-text channel), `public_places()`-gated at write AND re-validated at apply. inv.2: rides
`file_event_report`'s existing idempotency + rate-limit; `moved_suggestions` returns counts-only, no reporter
identity, no per-user history; moderator-applied (never auto-repin, no brigading lever). inv.4/inv.3: the
suggested place must already pass `public_places()` (no pending/USER place leaks; events are cohort-agnostic
public data). THE AT-RISK INVARIANT is inv.6 (ingest-safety) on the APPLY half: `upsert_event`
(apps/events/services.py:22) puts `place` in `defaults`, so a moderator re-pin of `Event.place` is CLOBBERED on
the next re-ingest for ICAL/GOOGLE feed events — a false-promise bug isomorphic to the W3-F16 false-retention
HIGH the reviewer already caught.

**Sketch.** Extend `EventReport` (apps/events/models.py:107) with `suggested_place = FK(places.Place,
null=True, on_delete=SET_NULL)`, populated only for `kind=MOVED`. Extend `file_event_report`
(apps/events/services.py:131) to accept an optional `suggested_place`, validating
`public_places().filter(pk=...).exists()` before storing. Add `moved_suggestions(event)` returning the top
suggested place by count (counts-only, no reporter identities) for the staff queue. Web wiring rides the
existing `event_report` POST (apps/web/views.py:2648); the form gains an optional place picker drawn from
`public_places()`. LOAD-BEARING RESHAPE: the moderator APPLY path must NOT write a clobberable `Event.place` on
a feed-sourced event. Two acceptable forms: (a) gate the re-pin so it only writes `Event.place` for
`source in {USER, MANUAL}` (where no re-ingest overwrites it), and for ICAL/GOOGLE store the chosen place in an
ingest-safe place-override overlay that `upsert_event` reads as a precedence layer (mirror how
`is_disputed`/`OpenNowReport` survive re-ingest) and is annotated at read time; OR (b) keep it
suggestion-only — `moved_suggestions` stays a moderator HINT with NO automatic `Event.place` write. Either way:
re-validate `public_places().filter(pk=...).exists()` at apply time, and a direct `Event.place` write on an
ICAL/GOOGLE event is forbidden.

**Depends on:** `EventReport`/`file_event_report` (F21); `places.public_places` chokepoint
**Touches:** apps/events/models.py; apps/events/services.py; apps/web/views.py; apps/web/forms.py; apps/web/templates/web/event_detail.html; apps/events/tests/

### F16 — Crowd duplicate-venue flag ("this is the same place as…")  `[M/imp3/low/revise]`
*Theme: Place & event data quality*

**Pitch.** The automated `dedup_places` command only merges cross-source pairs that are close +
name-similar; real-world duplicates with divergent names or just-over-threshold distance slip through and
split a venue's facts, events and activities across two rows. This lets verified members flag two published
places as the same venue, accruing a quorum into a staff merge candidate — closing the gap the geometric dedup
can't reach.

**Why it fits the invariants.** inv.6: Postgres-primary, a direct clone of the F26 `ActivityEdgeVote` /
W3-F13 `PlaceClosureReport` overlay — one new overlay table, no ML, ingest-safe (lives in its own table, never
on Place). inv.2: counts-only display, idempotent + rate-limited via `allow_action` exactly like
`file_closure_report` (apps/places/services.py:414); merge stays STAFF-applied through the existing
`merge_places` (no griefing — a quorum only *proposes*). inv.4: no PII; both places are public gazetteer rows.
No cohort/child surface touched. Sharp edge: must canonicalise pair order, gate both places through
`public_places()`, refuse self-pairs / already-merged rows, never expose reporter identities.

**Sketch.** New `PlaceDuplicateReport(place_low, place_high, reporter, created_at)` beside `PlaceClosureReport`
(apps/places/models.py:301). New `file_duplicate_report(reporter, place, other)` modelled line-for-line on
`file_closure_report` (services.py:414): `can_participate` gate, `allow_action` rate-limit, decay-window
idempotency, both-published check via `public_places()`. A `duplicate_candidates(threshold)` read for the staff
queue; staff resolves via the existing `merge_places` (apps/places/enrichment/dedup.py:89 — NOT dedup_places.py
as first claimed). Web button on `place_detail` lets a member name the duplicate from a `public_places()`
picker. LOAD-BEARING RESHAPE: three corrections. (1) Drop the proposed `UniqueConstraint(place_low, place_high,
reporter)` — `PlaceClosureReport` deliberately has NO `UniqueConstraint` because uniqueness is TEMPORAL (one
per reporter/pair/decay-window, enforced in the service) so post-decay re-reporting and self-heal work; a hard
constraint breaks decay. (2) `file_duplicate_report` must gate BOTH `place` and `other` through
`public_places()` at file time (refuse a pending USER place or a closure-hidden one), draw the web picker from
`public_places()`, reject self-pairs (`place==other`), and canonicalise to `(min(pk),max(pk))` so A→B and B→A
collapse to one tally. (3) `duplicate_candidates` must filter out candidates whose either side has been
deleted/merged away, and must NOT auto-merge — it only feeds the staff queue.

**Depends on:** `places.merge_places`; the `file_closure_report` overlay pattern (W3-F13); `public_places` chokepoint
**Touches:** apps/places/models.py; apps/places/services.py; apps/places/migrations/; apps/web/views.py; apps/web/templates/web/place_detail.html; apps/places/tests/

### F17 — Stale-source freshness signal ("last confirmed in open data")  `[M/imp2/low/revise]`
*Theme: Place & event data quality*

**Pitch.** An honest read-time freshness state for every venue, derived from how long ago the ingest pipeline
last saw it in its open-data source. Helps organisers and members judge whether a venue's facts are likely
current before they commit a meetup there, and surfaces silently-vanished OSM/Overture records that today keep
a fresh-looking but actually orphaned row.

**Why it fits the invariants.** inv.6/Postgres-primary: a read-time derivation off the EXISTING
`Place.last_seen_at` plus a small ingest-run marker — no write-back to Place, re-ingest-safe. inv.2: a neutral
tristate fact ("confirmed recently" / "not seen lately" / "user-added"), NOT a vanity count or freshness
leaderboard; never per-user. inv.4: no PII, no location stored. inv.1: text chip only. USER-source places
legitimately have no open-data heartbeat → short-circuit to "user_added" (the enum has only OSM/OVERTURE/
GOOGLE/USER, so the original sketch's "MANUAL too" is moot). THE HONESTY DEFECT (load-bearing): `last_seen_at`
is `auto_now`, and `ingest_places` is OPERATOR-RUN (NOT in DUE_JOBS, no fixed cadence) and a pure per-record
upsert with NO sweep marking "expected-but-absent this run."

**Sketch.** Add `place_freshness(place, *, now=None) -> str` beside `open_now_status`/`place_is_closed`
(apps/places/services.py:322): return 'fresh'/'stale'/'user_added' (USER source short-circuits). Display-only —
render a chip in `place_detail` (apps/web/views.py:986, alongside `open_now`/`venue_facts`) and the JS-free
/places/list/ fallback. A short staff doc note clarifies stale ≠ closed (closure is the W3-F13 crowd overlay).
LOAD-BEARING RESHAPE: a bare time-since-`last_seen_at` threshold measures operator ingest SCHEDULING, not
source freshness — right after a single seed ingest every place reads "stale" in lockstep (self-discrediting
noise), a wholesale re-ingest resets everything, and the pitch's headline (surfacing a silently-vanished OSM
record) is exactly what it CANNOT do, because a vanished record and a never-re-ingested area are identical on
the timestamp. Record per-ingest-run provenance — an `IngestRun` row (or per-area `last_full_ingest_at` marker)
— so `place_freshness` compares a place's `last_seen_at` against the MOST RECENT ingest run that COVERED its
area; older than the latest covering run = genuinely absent from the source = honest "not seen in the last
sync." This makes it a real orphan signal (no longer a zero-write pure read — needs the run marker, hence M);
impact stays modest given the operator-run cadence limits how often the signal even changes.

**Depends on:** `Place.last_seen_at` (`ingest_places` upsert); composes with W3-F13 closure overlay (distinct: stale=source-silence, closed=crowd-reported)
**Touches:** apps/places/services.py; apps/web/views.py; apps/web/templates/web/place_detail.html; apps/places/tests/test_services.py

### F18 — Self-only "my upcoming venues need a check" data-quality digest  `[S/imp3/low/keep]`
*Theme: Place & event data quality*

**Pitch.** A read-only panel that, for the meetups a user is actually a member of, flags any whose venue
currently reads unverified-hours, crowd-reported-closed, stale-in-source, or has a pending correction — so
organisers and attendees see venue data-quality problems for places they're about to go to, in one place,
before they head out. No new signals, just a personalised cross-section of existing ones.

**Why it fits the invariants.** inv.2: self-scoped, read-only, no per-user history stored; nothing is
persisted, and the only counts shown are the venue's own existing crowd tallies that `pending_corrections`
already exposes on place_detail (counts-only, no proposer identity); not a notification (no pestering) — it
lives on a page. inv.3: `_my_upcoming_meetups` is keyed `memberships__user=user` + `cohort=user.cohort`, so it
returns only the VIEWER's own meetups; a guardian sees their own, never the ward's (the safe outcome). inv.4:
all states are read-time derived, nothing persisted. inv.6: pure read aggregation, bounded queries,
Postgres-primary, no ML. inv.5: flags steer the viewer to a sound real place before heading out.

**Sketch.** Add a read-only `web` view (e.g. `/my-venues/`) that walks the viewer's upcoming
Membership→Activity→Place reusing `_my_upcoming_meetups` verbatim (apps/web/views.py:2803), and for each
distinct place collects existing states: `open_now_status`, `place_is_closed`, `pending_corrections`
(apps/places/services.py:322/399/740), and the new `place_freshness` (F17 — works without it too). Render
member-only flagged rows ("hours unverified", "reported closed", "not seen in source lately", "a correction is
pending"). LOAD-BEARING CONSTRAINT (already in the pitch, must stay): ship it as a PAGE only — never a DUE_JOBS
sweep or `notify()` call — so it can't become an engagement-nudge; reuse `_my_upcoming_meetups` verbatim (do
not re-derive visibility). The thin `venue_quality_flags(place)` helper must compose the existing read fns
only, adding no model and no stored state.

**Depends on:** `open_now_status`/`place_is_closed`/`pending_corrections` (shipped); `place_freshness` (F17 — optional)
**Touches:** apps/web/views.py; apps/web/templates/web/my_venues.html; apps/web/urls.py; apps/places/services.py; apps/web/tests/

### F19 — Crowd-correctable accessibility facts  `[M/imp3/med/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** Lets members report a venue's real step-free access, accessible toilet, and hearing loop when the
OSM map is silent or wrong — the same way they already crowd-confirm drinking water or fencing. Helps
wheelchair users, deaf/hard-of-hearing members, and parents who today see only "not recorded" for the facts
that matter most to whether they can even get in the door.

**Why it fits the invariants.** inv.6: reuses the existing ingest-safe `PlaceFactVote` overlay + quorum
decay verbatim. inv.4: venue facts only, fixed closed allowlist, bare booleans, no user location, no PII; the
model docstring already states co-voting is NOT a shared activity so it never enables `can_connect`
(child-safety wall untouched, inv.3). inv.2: counts-only pending UI, no per-user history. THE AT-RISK
INVARIANT IS SAFETY-OF-CLAIM (a sub-inv.3/accuracy concern): a false "step-free/accessible" badge routing a
wheelchair or deaf member to an inaccessible venue is a real-world harm. `accessibility_facts` is
OSM-AUTHORITATIVE today (`place_fact_status` returns the OSM tristate and never consults crowd votes for
step_free); naively adding ACCESS keys would let a quorum OVERRIDE a real OSM `wheelchair=yes` tag, AND
`_crowd_state` returns `FACT_TRUE` on a quorum of yes-votes which `matches_access_preference` turns into a
"match" badge — so crowd say-so alone would assert accessible.

**Sketch.** Add three keys to `PlaceFactVote.FactKey` (apps/places/models.py:341) —
STEP_FREE/ACCESSIBLE_TOILET/HEARING_LOOP — and map them in `_FACT_OSM` (apps/places/services.py:462) to their
`wheelchair`/`toilets:wheelchair`/`hearing_loop` OSM tags so `place_fact_status` (services.py:510) keeps OSM
authoritative and crowd votes only fill unknowns. Surface via the existing `venue_facts_detail` + `vote_on_fact`
gate (`can_participate`, `public_places()`, rate limit). No-op `makemigrations places` for the enum.
LOAD-BEARING RESHAPE: two-part. (1) WIRE INTO THE SURFACE USERS ACTUALLY READ — the new ACCESS keys must feed
`accessibility_facts()`/`accessibility_facts_display()` and thus `matches_access_preference` + the /access/
sort, not live only on the separate `venue_facts()` list; add an OSM-first-then-crowd merge to
`accessibility_facts` (mirror `place_fact_status`) so a crowd-confirmed fact updates the SAME badge/sort the
access preference drives, or you ship a second mute accessibility surface and the inclusion value never lands.
(2) FAIL-CLOSED ON THE POSITIVE DIRECTION — for ACCESS facts the crowd overlay may surface a caution (crowd
'no'/'limited' where OSM is silent) but must NEVER produce a positive `FACT_TRUE`/"match" badge from crowd
votes alone; add an access-fact branch capping crowd positives at `FACT_LIMITED/UNKNOWN`. Only a real OSM
`wheelchair=yes`/`hearing_loop=yes` tag may assert accessible; crowd never overrides an existing OSM tag.

**Depends on:** F19 crowd venue facts (`PlaceFactVote`/`vote_on_fact`); F15/F32 `accessibility_facts`/`matches_access_preference`
**Touches:** apps/places/models.py; apps/places/services.py; apps/places/tests/; apps/places/migrations/

### F20 — Sensory/pace 'what to expect' facet (calm vs lively, set pace)  `[M/imp3/low/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** Adds honest organizer-stated chips for sensory load (quiet/calm vs loud/lively) and whether the
group moves at the pace of the slowest member — the information autistic members, people with sensory
sensitivities, and slower-paced or older participants need to self-select in. Finally gives a stated quiet/pace
preference an honest signal to match against.

**Why it fits the invariants.** inv.2: facts are organizer-DECLARED enums shown as chips, no rating/rollup/
behavioural inference. inv.4: about the activity, not a user; the matching preference is a stated setting, no
PII/location. inv.6: two small `TextChoices`, no ML, Postgres-only. The soft badge must mirror
`matches_access_preference` (apps/places/services.py:210): returns match/unknown only and NEVER excludes
unknown-sensory activities. Clears the prior rejection: the dead `prefers_quiet` verdict was VENUE-level ("no
honest OSM source", places/models.py:189); the organizer stating their own activity's character is a genuinely
knowable declared fact — a different, legitimate source.

**Sketch.** Add `Activity.sensory_level` and `Activity.inclusive_pace` as `TextChoices` modelled on
`Activity.difficulty` (apps/social/models.py:83), threaded through `create_activity`/`update_activity`
(services.py:626, the F8 whitelist). Render chips next to the F8 row. Web forms.py + /access/ edit view.
Migrations, tests. LOAD-BEARING RESHAPE: do NOT match against the existing venue-level
`AccessPreference.prefers_quiet` (it stays stored-and-dead per the standing verdict — it is a VENUE axis, not
an activity-character axis; matching it against an activity-sensory chip is a misleading cross-axis claim).
Instead the soft badge matches ONLY a NEW same-axis stated preference (e.g.
`AccessPreference.prefers_calm_activity`/`needs_inclusive_pace`) against the new `Activity.sensory_level`/
`inclusive_pace` chips. The display chips ship unconditionally; the badge fires only on this new same-axis
stated setting and, like `matches_access_preference`, never hides unknown-sensory activities.

**Depends on:** F8 what-to-expect fields; F15/F32 `AccessPreference` + `matches_access_preference`
**Touches:** apps/social/models.py; apps/social/services.py; apps/places/models.py; apps/places/services.py; apps/web/forms.py; apps/web/views.py; migrations; tests

### F21 — Spoken-language facet on activities + follow-along filter  `[S/imp3/low/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** Lets an organizer declare which language(s) a meetup is actually run in (Romanian, Hungarian,
English — Cluj is trilingual) and lets a member filter the feed to activities they can follow. Helps the large
Hungarian-speaking minority, expats, and refugees who today can't tell from an address pin whether they'll be
able to join in.

**Why it fits the invariants.** inv.6: a tiny fixed enum set on the F2 edit path — no ML, no new datastore.
inv.5: it makes the in-person meetup MORE joinable, never an online surface. inv.2: an honest organizer-stated
facet shown as a chip + an opt-in filter, NOT a ranking boost or vanity count; the filter must be additive/
opt-in like the beginners filter (services.py:230 + web/views.py:828 keep the ranked strip unfiltered), and the
field must be a constrained enum NOT free text (free text = unscannable covert channel, breaks the
cohort-visible chip). inv.4: the activity's language is event metadata, not user PII; a per-user preferred
language would be a STATED setting like `AccessPreference`, not inferred. Sharp edge: must route through
`update_activity`'s whitelist (`ACTIVITY_EDITABLE_FIELDS`) like `cost_band`/`difficulty`.

**Sketch.** Add `Activity.languages` threaded through `create_activity`/`update_activity`
(apps/social/services.py:626, the editable-field whitelist) exactly as the F8 what-to-expect chips were. Add a
`?language=` filter alongside the beginners filter (services.py:230). Render as a cohort-visible chip in the
same row as `cost_band`/`difficulty`. Web ActivityForm/ActivityEditForm. No-op `makemigrations social`.
LOAD-BEARING RESHAPE: model MULTI-VALUE, not a single CharField — Cluj meetups are routinely bilingual (RO+HU is
the inclusion point), so a single-value `TextChoices` forces a false "one language only" chip that dents inv.2
honesty. Use a fixed-enum multi-value field — `ArrayField(CharField(choices=Language))` validated against the
enum (still scannable, still a cohort-visible chip set, no new datastore), enum defined ON the model and
INCLUDING 'hu' (do NOT reuse `config` LANGUAGES at base.py:121 — it is only [en, ro]; Hungarian, the actual
inclusion target, is absent there). The `?language=` filter must be additive/opt-in exactly like
`?beginners=true`, never narrowing the ranked strip.

**Depends on:** F8 what-to-expect fields; F17 beginners filter
**Touches:** apps/social/models.py; apps/social/services.py; apps/web/forms.py; apps/web/templates/; apps/social/migrations/; apps/social/tests/

### F30 — Support-person companion seat  `[S/imp3/low/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** Lets a member who needs a personal assistant, carer, or sign-language interpreter to participate
bring that one support person as a non-counted, non-discoverable companion seat — so the accompanying person
isn't competing for a capacity slot and isn't forced into the social discovery layer themselves. Helps disabled
adults and people who can only attend with a designated supporter.

**Why it fits the invariants.** inv.5/in-person: the whole point is enabling a real attendance that's
otherwise blocked. inv.2: a single per-membership boolean reset on leave, like `welcomed_at`/`met_confirmed_at`,
never aggregated cross-activity. inv.3: a companion is NEVER a second User, never contactable, never in any
feed/thread/connection/messaging path, so it cannot be a cross-cohort or adult→minor backchannel —
structurally holds even before the cohort allowlist; ADULTS-ONLY at launch (gate on cohort like Connections'
`CONNECTIONS_ALLOWED_COHORTS`) is belt-and-suspenders. inv.4: no PII beyond a boolean on the member's own row.
inv.6: one boolean, no ML. AT RISK is capacity honesty: the originally-pitched "adjust `seats_remaining`" option
would let an accessibility declaration silently consume a member seat and BLOCK other joiners (can_join:504) and
break the `min_to_go<=capacity` rule.

**Sketch.** Add `Membership.brings_support_person` boolean (apps/social/models.py:266, alongside
`welcomed_at`/`met_confirmed_at`). A `set_support_companion` service gated on `can_participate` + cohort
allowlist (reuse the Connections cohort-gate pattern). Web: a checkbox on the join/RSVP surface. One migration,
tests. LOAD-BEARING RESHAPE: companions are NOT capacity-counted — leave
`participant_count`/`open_positions`/`can_join` (apps/social/services.py:485-504) COMPLETELY unchanged so
capacity stays a count of MEMBER POSITIONS only. Surface companions ONLY as a separate organizer-only
"N members bringing a support person" logistical line in `organizer_console` (services.py:303) — never folded
into `seats_remaining`, never shown to members as a count, never on a discovery surface. This avoids (a) a lying
capacity number, (b) an access need silently costing a seat or blocking other members' joins, and (c) a
discrimination vector. Drop the "adjust seats_remaining" alternative entirely.

**Depends on:** `Membership`/`can_join`/`participant_count`; W3-F5 organizer prep console
**Touches:** apps/social/models.py; apps/social/services.py; apps/web/views.py; apps/web/templates/; migrations; tests

### F22 — Complete-the-export: your DSA safety record + privacy settings in the Art.20 download  `[S/imp3/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** The one-click GDPR Art.20 export (F35) covers profile/memberships/posts/donations but silently
OMITS the user's own DSA Art.16/17 moderation record, the reports they filed, their blocklist, and their
notification-mute settings — data the platform demonstrably holds about them and shows on-screen, yet won't
hand over portably. This closes the portability gap so "download my data" is honestly complete.

**Why it fits the invariants.** inv.4: each new section reuses an EXISTING self-scoped, field-allowlisted
read — `safety.safety_record_for` (apps/safety/services.py:631, already strips moderator identity/notes/
who-reported), `notifications.get_muted_kinds`, and the user's own `Block` rows (web/views.py:2321 already shows
these to the blocker on /blocks) — so it can NEVER widen exposure beyond what those services already permit
on-screen; it never exposes another user's data or a moderator. inv.2: no behavioural data fabricated — only
data already lawfully held. inv.6: pure Python dict assembly, no new model, no ML. Sharp edge: the export must
route through the SAME hardened self-scoped functions, never re-query the ORM rows directly (the
`safety_record_for` docstring warns the raw rows with FKs must never leave the function); a test must assert the
new sections contain no moderator/target identifiers, and bump `EXPORT_SCHEMA_VERSION` to 3.

**Sketch.** Extend `build_user_export` (apps/accounts/export.py:19) with `_safety_record(user)` calling
`apps.safety.services.safety_record_for`, `_blocks(user)` projecting the user's own `Block` rows to `{when}`
(never the blocked party's identity beyond what /blocks already shows), and `_privacy_settings(user)` from
`notifications.get_muted_kinds` + the `AccessPreference`. Bump `EXPORT_SCHEMA_VERSION` (currently 2) to 3 with a
comment. No view change needed — `account_export` (apps/web/views.py:3445) and the DRF `/me/export/` endpoint
both already serialize whatever `build_user_export` returns.

**Depends on:** `build_user_export` (F35/W2-F32); `safety.safety_record_for` (F19); `notifications.get_muted_kinds` (F31)
**Touches:** apps/accounts/export.py; apps/accounts/tests/test_export.py

### F23 — Where-your-data-lives recipient register (Art.13/30 legibility)  `[S/imp3/low/revise]`
*Theme: Privacy & data-dignity as product*

**Pitch.** A self-scoped, derived "who can touch your data and where it lives" panel that turns the
platform's lean EU-hosted, no-third-party-tracking posture into a felt, verifiable surface. Complements the
W3-F16 retention clock (how long) with the missing GDPR Art.13(1)(e)/Art.30 dimension: who processes it, where,
and why. Helps every privacy-conscious adult and every guardian deciding whether to consent a child.

**Why it fits the invariants.** inv.4 (load-bearing): every recipient row is DERIVED from a live config truth
(object-storage endpoint region, the configured identity provider via `get_identity_provider()`, whether a
real donations provider is wired, `REDIS_URL` presence for chat transport) — it can never publish a FALSE "we
don't share with X" claim, mirroring `retention_disclosure`'s honest-null discipline. No PII, no location, no
cohort leak. inv.2: pure read, donations-funded framing, no tracking. inv.6: reads Django settings/constants
only, Postgres-incidental, zero ML. NOT a duplicate: `privacy.html` is a generic legal page that does not
enumerate per-recipient region/purpose; `retention_disclosure` covers Art.5(e) durations only.

**Sketch.** Add `accounts.data_recipients(user) -> list[{recipient, role, region, purpose, present: bool}]`
next to `retention_disclosure` (apps/accounts/services.py:1012). Each row derives presence from a live setting:
object storage region (`MEDIA_S3_REGION`/`MEDIA_S3_ENDPOINT_URL`), identity/age provider from
`get_identity_provider()`, payments from the configured donations provider, real-time transport from
`settings.REDIS_URL`. Render in a new section of the existing `my_privacy` view (apps/web/views.py:3347) +
my_privacy.html, alongside the retention list it already passes. The static `privacy.html` stays the legal
page; this is the personalised derived mirror. LOAD-BEARING RESHAPE: the donations row must derive presence/
recipient/region from `get_payment_provider().name` (deeplink = external nonprofit checkout host via
`DONATIONS_CHECKOUT_BASE_URL`; dev = synthetic, no external call; stripe = Stripe EU) — NOT a hardcoded "no
payment processor connected", which is itself a latent false claim since the default DeepLinkProvider DOES route
the donor off-platform. AND the fail-closed coverage TEST must be the actual gate: assert every configured
external integration (`MEDIA_STORAGE_BACKEND`, `IDENTITY_PROVIDER`, `get_payment_provider().name`, `REDIS_URL`
presence) maps to exactly one register row, so a future Stripe-prod flip or 2nd source cannot silently
under-disclose and turn the panel into a lie.

**Depends on:** `retention_disclosure` (W3-F16) + the `my_privacy` front-door (F36); reuses `identity.registry.get_identity_provider`
**Touches:** apps/accounts/services.py; apps/web/views.py; apps/web/templates/web/my_privacy.html; apps/accounts/tests/test_data_recipients.py

### F25 — Disappearing-photo self-purge receipt (storage-limitation proof)  `[M/imp2/high/revise]`
*Theme: Privacy & data-dignity as product*

**Pitch.** When a user's ephemeral photo is auto-deleted by a DUE_JOB, there is currently no honest,
after-the-fact confirmation TO THE USER that the platform actually did what its retention clock promised. A
calm, self-only "we deleted this on schedule" receipt makes GDPR Art.5(1)(e) storage-limitation a felt,
verifiable event rather than a claim — turning data-minimisation into a trust signal.

**Why it fits the invariants.** inv.4: the receipt records ONLY that a category self-deleted and when — never
the deleted content, never a location, never reconstructible data. inv.2: NOT a notification-bait re-ping; a
passive row on the existing /my-privacy/ page (no fan-out). inv.6: a tiny Postgres timestamp written by the
existing purge job, no ML. THE AT-RISK INVARIANT IS inv.2 AND IT DOES NOT HOLD as first sketched: a
`PurgeReceipt(user, category=arrival, lifetime_count, last_purged_at)` is, for the arrival/on-my-way category, a
per-user cumulative count of how many meetups the user set an arrival ping at — exactly the banned per-user
reliability/attendance/presence rollup (W1-F43, W2-F30/F39, W3 note 7) and exactly the standing presence record
the arrival-ping model (apps/social/models.py:311-324) is built to never become. Plus `expire_arrivals` is one
bulk `.update()` with NO per-user loop, and `purge_expired_messages` iterates per-conversation with bulk
`.delete()`, so a per-user-per-category receipt requires rewriting deliberately-bulk ephemeral jobs.

**Sketch.** LOAD-BEARING RESHAPE: drop the arrival/on-my-way category entirely (it is the inv.2 violation and
the bulk-update seam that isn't per-user) AND drop the per-user `lifetime_count` counter. Ship ONLY an
AGGREGATE, non-per-user confirmation for the genuinely per-item photo-attachment purge
(`purge_expired_attachments`, the one per-item job at apps/media/services.py:546) — i.e. "temporary pictures you
posted are deleted on schedule; the last sweep ran on DATE" as a felt complement to the `retention_disclosure`
clock, with NO cumulative count and NO arrival category, surfaced on `my_privacy` (apps/web/views.py:3347). The
receipt write must be INSIDE the purge job's transaction so a rolled-back purge records nothing — a receipt of
a deletion that didn't happen is a worse lie than silence. Even reshaped this overlaps W3-F16 heavily and the
impact is marginal (hence impact 2, high-risk-if-built-naively verdict).

**Depends on:** `retention_disclosure` (W3-F16); the `purge_expired_attachments` media purge job; surfaces on the F36 front-door
**Touches:** apps/accounts/models.py; apps/media/services.py; apps/web/views.py; apps/web/templates/web/my_privacy.html; migration + tests

### F26 — Profile-field minimisation control (data-dignity by subtraction)  `[S/imp3/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** A user can ADD a display name and a profile picture, but there's no first-class, self-service way
to LATER strip optional profile fields back to the bare minimum (clear display_name, remove the single profile
picture) without deleting the whole account. A "minimise my profile" control makes GDPR Art.5(1)(c)
data-minimisation and Art.16/17 (rectification/partial erasure) a one-click reality short of nuking everything.

**Why it fits the invariants.** inv.4 (primary driver): pure SUBTRACTION of optional user-supplied PII —
strongest possible minimisation alignment; collects nothing new. inv.1: only ever removes the one profile
photo, never adds an image surface. inv.3 (at-risk): blanking must NEVER touch
`age_band`/`cohort`/`role`/`is_identity_verified` (models.py:74-81) — cohort drives isolation and
`is_identity_verified` drives `can_participate` + the reverify sweep; the immutable-field assertion test is
load-bearing, so the invariant holds as scoped. inv.2/inv.6: audited via `record_audit`, no metric, thin
Postgres-only service reusing the existing Photo pipeline, no ML.

**Sketch.** Add `accounts.minimise_profile(user)` in apps/accounts/services.py: `@transaction.atomic`, blank
`display_name`, delete the user's profile Photo through the existing media services (the same pipeline behind
`upload_photo`/`delete_photo` at apps/media/services.py:184), call `record_audit('accounts.profile_minimised')`.
Wire a confirm-then-POST control onto the existing profile dashboard / settings web view (the upload-only
`avatar_upload` at apps/web/views.py:2343 has no removal path today), and deep-link it from `my_privacy`
(apps/web/views.py:3347). The change is visible in the F34 activity_log automatically via the audit row. A test
asserts the immutable safety/identity fields (`age_band`/`cohort`/`role`/`is_identity_verified`) are untouched.

**Depends on:** the media Photo pipeline (`upload_photo`/`delete_photo`); `record_audit`; the F34 activity log; the F36 privacy front-door
**Touches:** apps/accounts/services.py; apps/web/views.py; apps/web/templates (profile/settings + my_privacy); apps/accounts/tests/test_minimise_profile.py

### F28 — Ward-side observation transparency log (child sees when a guardian read their chat)  `[M/imp2/low/revise]`
*Theme: Privacy & data-dignity as product*

**Pitch.** Guardian messaging-observation is designed to be VISIBLE-in-chat, and the /guardianship/ panel
states a guardian "can read your group chats as a visible observer" — but a ward has no durable, self-scoped
record of WHEN observation was actually enabled or ended on their conversations. A calm ward-readable
observation log upholds the child's own data-dignity inside a lawful supervisory relationship.

**Why it fits the invariants.** inv.3: observation already keys on an ACTIVE `GuardianRelationship` and is
visible-in-chat by design; this only makes the existing observer events legible to the supervised minor — it
never weakens the wall, reveals no message content, and creates no cross-cohort contact. inv.4: self-scoped to
the ward; shows only observation lifecycle events (enabled/ended + when + which conversation), no message
bodies, derived from existing audit events, field-allowlisted exactly like `safety_record_for`. inv.2: passive
read, no metric, no fan-out. inv.6: reads existing messaging audit rows — no new write path, no ML. Sharp edge:
it must be strictly the WARD's own view of observation OF THEM (never a sibling's observation or the guardian's
other wards); legibility-only (a ward can't unlink, matching F13's read-only ward side). NOT a duplicate: F13
/guardianship/ is a STATIC capability line; F34 `audit_log_for` is filtered to `actor_ref=user.id` (the ward's
OWN actions), never observation-of-them.

**Sketch.** Add a self-scoped read `ward_observation_log(ward)` projecting the existing audit events around
`add_guardian_observer`/`drop_guardian_observers_for` (apps/messaging/services.py:421/456/477) to allowlisted
`{when, conversation_label, state}`. Surface on the ward-facing /guardianship/ page (already rendered with
`caps.can_observe_messaging`, guardianship.html:16). LOAD-BEARING RESHAPE: the audit row carries
`actor=guardian` + `data.conversation_id`, NOT the ward. In a GROUP conversation containing CHILD members from
different families, a naive conversation-scoped projection would surface observation triggered by ANOTHER
child's guardian (cross-family leak) and mislabel it as observation "of you." Query
`AuditLog.objects.filter(actor_ref__in=[ids of THIS ward's currently-ACTIVE guardians],
event__in=["messaging.guardian_observing","messaging.guardian_observer_ended"])` — uses the indexed `actor_ref`
column (cheap, inv.6) — then keep only rows whose `data.conversation_id` is a conversation the ward was an
active Participant of. Field-allowlisted exactly like `safety_record_for`. Also fix the event-name error: the
enable event is `messaging.guardian_observing`, not `guardian_observer_added`.

**Depends on:** guardian-observer messaging (`add_guardian_observer`/`drop_guardian_observers_for`); the F13 guardianship legibility panel; the `safety_record_for` allowlist pattern
**Touches:** apps/messaging/services.py (or apps/accounts/services.py); apps/web/views.py; apps/web/templates/web/guardianship.html; tests

### F24 — Civic-impact year-in-review (honest aggregate, no vanity)  `[S/imp2/low/keep]`
*Theme: Civic impact, transparency & sustainability*

**Pitch.** A staff-curated /transparency/impact/ section that pairs the existing money ledger with a small set
of staff-entered, plainly-worded `CivicOutcome` statements (e.g. "Library reading circles ran at 4 partner
venues this season") — narrative, period-labelled, never a live counter or "X of Y" bar. Helps donors and the
public see what the platform's spend and partner backing actually produced, in words a volunteer org controls.

**Why it fits the invariants.** inv.2 (the hard line): these are STAFF-AUTHORED TEXT statements, NOT a live
count of activities/users/attendance (which would be a vanity metric) — the model stores a prose claim +
optional period, mirroring `Campaign.outcome` and `SpendEntry`'s aggregate-only, donor-FK-free shape; no
per-user rollup. This is exactly what distinguished the prior-REJECTED auto-derived /impact proposals (which
needed k-anon suppression) — this candidate sidesteps that class entirely and is NOT a duplicate. inv.1
text-only. inv.4: no PII, no donor link (same constraint as `SpendEntry`); the optional `places.Partner` FK
reuses the proven `public()` clean()-gate. inv.6: plain Postgres rows + a flat read aggregate, no ML. Sharp
edge: it must NOT auto-derive numbers from Activity/Membership tables.

**Sketch.** Add a `CivicOutcome` model in apps/donations/models.py cloning `InKindContribution`'s shape (line
192) minus quantity: fields `headline` (CharField cap 280), `detail` (cap 300), `period` (free-text label),
optional `partner` FK to `places.Partner` SET_NULL with the same `clean()` `public()`-gate (line 227). Add
`donations.services.civic_outcomes()` returning plain dicts ordered by `-created_at` (like `in_kind_by_category`
at services.py:170). Render a third, clearly-separated section in `transparency()` (apps/web/views.py:2494) +
transparency.html, kept apart from money sections exactly as `in_kind_rows` already is. Admin entry like
`InKindContributionAdmin`. One migration. LOAD-BEARING RESHAPE: ships as sketched; lock the invariant in with a
TEST (not a design change): assert `CivicOutcome` has no FK/query path to Activity/Membership/Donation and the
/transparency/impact/ section renders stored prose only (no computed count/total) — mirroring
`in_kind_by_category`'s donor-FK-free shape.

**Depends on:** F29 donation transparency; W3-F20 InKindContribution; F42 partner-credited campaign (the `public()` partner-gate pattern)
**Touches:** apps/donations/models.py; apps/donations/services.py; apps/donations/admin.py; apps/web/views.py; apps/web/templates/web/transparency.html; apps/donations/migrations/

### F27 — Cost-anchor delivery receipts (close the imagination→actuals loop honestly)  `[S/imp2/low/keep]`
*Theme: Civic impact, transparency & sustainability*

**Pitch.** Today `CostAnchor` shows donors "EUR 40 = one library room booking" purely illustratively,
deliberately never tied to actuals. This adds an OPTIONAL, staff-published "and here's one time we did exactly
that" link from an anchor to a real completed `SpendEntry`, so the donate page's promise is backed by a
verifiable past delivery — without ever becoming a live ratio or goal bar.

**Why it fits the invariants.** inv.2: explicitly NOT an "X of Y" bar and NOT a live computed ratio (the
`CostAnchor` docstring at apps/donations/models.py:104 forbids that) — instead a discrete, manually-published
one-to-one example linking an anchor to one historical `SpendEntry`; no countdown, no progress, no recompute
when new spend lands. inv.4: `SpendEntry` is donor-FK-free aggregate data, so nothing personal surfaces. inv.6:
a nullable FK + a `select_related` join, no ML. Sharp edge: the link must stay STAFF-SET and DECORATIVE — it
must not auto-sum or recompute; `CostAnchor.spend_category` stays the decorative label it is and the receipt is
a separate, explicit, optional FK the donor reads as "an example," not a fill bar.

**Sketch.** Add a nullable `example_spend` FK (SET_NULL) on `CostAnchor` (apps/donations/models.py:100)
pointing at one `SpendEntry`, set ONLY in `CostAnchorAdmin` (admin formfield limited + a `clean()` check it
exists). Extend `cost_anchors()` (apps/donations/services.py:188) to include the example's plain
category+period+note when present (`select_related` to avoid N+1). Render it as a calm "for example, we did this
in 2026 Q1" line beneath the anchor in the donate template via `donate()` (apps/web/views.py:2464, already
passes `cost_anchors()`). One migration; no new model. The invariant holds provided the example renders as the
`SpendEntry`'s own category/period/note (an illustration), never as "amount of amount delivered."

**Depends on:** W3-F19 CostAnchor; F29 SpendEntry
**Touches:** apps/donations/models.py; apps/donations/services.py; apps/donations/admin.py; apps/web/views.py; apps/web/templates/web/donate.html; apps/donations/migrations/

### F31 — Venue civic-need note (a partner's honest 'we could use a hand')  `[S/imp2/low/revise]`
*Theme: Civic impact, transparency & sustainability*

**Pitch.** A verified civic partner can publish one short, staff-moderated standing note on a venue it
stewards — e.g. "Our community garden welcomes weekend tidy-up groups" — surfaced as a calm acknowledgement
line that nudges organisers toward genuinely civic, give-back group activities at real partner places. Pure
adults-and-families discovery of where in-person volunteering-style group activity is wanted.

**Why it fits the invariants.** inv.1: text-only (a capped CharField, no image — Partner itself forbids
logos). inv.2: NOT advertising/pay-for-placement — it routes through the same `Partner.objects.public()`
chokepoint and neutral alphabetical/per-venue placement, no boost, no count; STAFF-edited via admin (not
user-submitted), so it holds PROVIDED the copy stays descriptive ("welcomes tidy-up groups"), not a recruiting/
urgency CTA. inv.3: venue-level public discovery, NOT contact — it creates no adult↔minor channel and no
roster; for a CHILD-cohort venue the note shows but joining still flows through the untouched F9 public-venue +
F29 supervisor gates. inv.5: steers real in-person give-back at real civic places. inv.6: one text field + a
`public()`-gated read, no ML.

**Sketch.** Surface via `partner_for_place(place)` (apps/places/services.py:295) on place_detail next to the
existing partner credit line. All reads stay behind `Partner.objects.public()`
(apps/places/models.py:198) so an unverified/deactivated partner's note vanishes. LOAD-BEARING RESHAPE: do NOT
add a new `civic_note` column — Partner already has a 280-capped `blurb` TextField (models.py:221) that is
stored but NOT currently surfaced on place_detail (only name/website/kind render at template line 15). Reuse/
surface `blurb` as the civic note (or treat the give-back line as the documented semantic of `blurb`), so this
becomes a render-only change (template + a one-line context note), zero migration, no redundant near-duplicate
field. Keep the copy a neutral descriptive credit, never an urgency/recruiting prompt.

**Depends on:** F37 Partner + `partner_for_place`
**Touches:** apps/places/services.py; apps/web/views.py; apps/web/templates/web/place_detail.html

## Rejected (not carried forward)

- **How-to-find-the-group field (rendezvous recognisability)** *(Reliability)* — duplicate-of-shipped: F41
  `Activity.first_time_note` is exactly this ("how to recognise the group, what happens first"), same edit path,
  member-gated; a second field would only fragment the surface.
- **Read-time 'is this a hard place to find?' venue facts hint** *(Reliability)* — infeasible: the OSM signals
  (entrances/extent/parent-relations) are NOT ingested (centroid + own tag dict only), so the honesty rule forces
  a venue-type-keyed constant guess that violates inv.2.
- **"Regularly hosts" honest venue-activity signal on place_detail** *(Discovery)* — duplicate-of-shipped:
  place_detail already renders an "Activities here" card with the identical non-disputed `PlaceActivity` edge
  filter; an honest frequency signal would need an inv.2 vanity count + inv.3 cross-cohort leak.
- **"Was here together" venue-anchored reconnect prompt** *(Discovery)* — duplicate-of-shipped: the connect
  button on the member roster already ships verbatim (guardian-excluded, `can_connect`-gated); the proposed
  COMPLETED-only narrowing would remove a working, invariant-clean affordance for a false reason.
- **'Help record this' accessibility contribution prompt** *(Accessibility)* — infeasible / unmet Phase-2
  dependency: it depends on the unshipped crowd-correctable-accessibility feature (F19 here); as sketched it would
  link to a form that silently cannot accept the data — a worse dead-end than the honest "not recorded."
- **Partner steward detail page + their venues** *(Civic impact)* — duplicate-of-shipped + infeasible-premise:
  `Partner.place` is a SINGLE FK (one place per partner), so "venues it stewards" (plural) is structurally false;
  every renderable fact is already on /partners/ and the place_detail credit.
- **Place sustainability facts (car-free reachability)** *(Civic impact)* — duplicate-of-shipped: W2-F22
  "getting there" already ships every honest fact (BIKE_PARKING/BUS_TRAM_NEARBY/CAR_PARKING) via the OSM-first +
  crowd overlay; the only non-shipped sub-fact ("walkable") has no honest backing tag. Saturated theme.

## Stats

31 vetted candidates across 8 themes (each adversarially read against the live code at the named seam): 10
keep, 21 revise (revise = ships only with its load-bearing reshape folded in), plus 7 rejected
(not-carried-forward) above. 1 at impact 4, 19 at impact 3, 11 at impact 2. Effort: 23 S, 8 M, 0 L. Risk: 28
low, 2 med (F15 moved-venue re-pin, F19 accessibility claim-safety), 1 high (F25 purge-receipt inv.2 landmine).
No new heavy/ML deps; all Postgres-primary; no Phase-2 / legal-gated bet in the starter set.
