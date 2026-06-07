# Feature catalog — 2026-06 ideation

> Produced by the feature-ideation-catalog workflow (105 agents): map → ideate (11 lenses)
> → cluster/reject invariant-violators → adversarial evaluate → synthesize. Built after public
> groups + place-proposal API + Romanian i18n shipped. Verdicts: keep / revise (revise = ships
> only with the load-bearing fix in its sketch). Effort S/M/L; impact 1-5; risk low/med/high.

## Recommended starter set: F1, F2, F6, F35

A coherent, low-risk first batch that hits the two pillars (the find→commit loop and the child-safety promise) while shipping fast. F1 (Quorum-go, S/low) and F2 (place picker, M/low) directly attack the core 'find people and go' funnel: F1 turns wobbly maybes into confident 'we're on', and F2 fixes the single biggest organiser friction (a flat global venue dropdown) — both reuse already-verified seams with no new GeoDjango/Channels work. F6 (re-verify-or-pause sweep, M/low) is the highest-leverage child-safety win that ships safely BEFORE minor onboarding flips on (it makes age-proof expiry active instead of lazy), and it establishes the SYSTEM-notice + sent-marker + DUE_JOBS pattern that F28 and others reuse. F35 (Download my data, S/low) is a near-mechanical clone of an existing hardened export builder that closes a felt GDPR-Art.20 gap and pairs with the privacy front-door (F36) later. All four are low-risk, individually shippable per the repo's per-feature branch→test→adversarial-review cycle, and none depends on another, so they can proceed in parallel.

**Quick wins:** F1, F8, F12, F14, F16, F17, F24, F31, F34, F35, F36, F37  ·  **Big bets:** F9, F22, F29

## Sequencing notes

SEQUENCING: (1) Several features only deliver value once minor onboarding is enabled (ALLOW_MINOR_ONBOARDING) with live ACTIVE guardian relationships — F7 (guardrails), F9 (public-venue gate), F29 (supervisor seat), F30 (minor-group relief valve). Build F6 (re-verify sweep) and the F8 panic button FIRST so the child-safety floor is active before onboarding flips on; F7/F9/F29 should land in the same wave as onboarding, not before. (2) NOTIFICATION KINDS: F1, F3, F16, F17, F27, F28, F30 each add a new MUTABLE Notification.Kind and need a no-op `makemigrations notifications` to keep CI green; batch the kind additions to avoid migration churn. F8 and F6 deliberately reuse the existing non-mutable SYSTEM kind (a mutable safety alert is a defeat). (3) DUE_JOBS: F3, F4, F6, F27 all append a periodic command to the ops DUE_JOBS tuple and must update its membership test — coordinate so they don't collide. (4) OVERLAY PATTERN: F19, F20, F21 all clone the F26/F28 ingest-safe quorum/decay overlay; build one first (F20 is the cleanest single-property win) to settle the shared pattern, then the others follow cheaply. (5) DEPENDENCY CHAINS: F38 (offline card) reuses the self-scoped query shape and guardian/meeting-point context that F2's place work and the wards query expose; sequence it after. F42 depends on F34/F37 (both shipped). F23 (recurring gifts) and F24 (fiscal receipts) should ship bundled with the Stripe-provider prod activation — F23 is INERT until that flip, and both need real ORG_LEGAL_*/legal sign-off. (6) REVISE DISCIPLINE: the 'revise' verdicts are not soft — each carries a load-bearing fix that is the condition of shipping (e.g. F3 must drop the stored pin entirely; F8 must use SYSTEM not a mutable kind AND add the missing web-path throttle; F9 must build its own allowlist not reuse GENERIC_VENUES and handle unknown-class without over-blocking; F22/F29 must restrict peer transfer/supervision to ADULT cohorts; F40/F41 must avoid minting redundant Activity fields; F43 is mostly already-built — only the one-line auto-open delta is real). Do NOT skip these in implementation. (7) AVOID DOUBLE-COUNTING: F40 and F41 overlap with the shipped F17 beginners_welcome and F9 logistics fields respectively — resolve the 'distinct field vs relabel' question before writing code, or they dilute the card rather than add to it."}

## Themes

- **Closing the find→commit loop** (F1, F2, F3, F27) — Convert latent intent into committed in-person attendance — make matching meetups easier to find, propose, and confirm in a thin launch city.
- **Filling every seat fairly** (F4, F14, F22, F26) — Keep thriving meetups alive and seats reclaimable across cancellations, churn, repetition, and volunteer turnover.
- **Actually showing up** (F15, F16, F17, F28) — Get committed people to the right spot, on time, prepared — and reassure guardians and peers around the meetup itself.
- **Deepening the child-safety promise** (F6, F7, F8, F9, F18, F29) — Tighten the core promise: active expiry enforcement, guardian-set limits, public-venue gates, supervisor seats, and a panic button — all keyed on ACTIVE guardian relationships, never loose flags.
- **Belonging for newcomers** (F30, F39, F40, F41) — Lower the social drop-at-the-door barrier for shy first-timers and give muted minor groups an honest voice.
- **Accessibility & inclusion** (F5, F12, F31, F32, F38) — Widen who can use the find-people-and-go loop — proximity-aware ranking, themes, pictographs, accessibility facts, and offline resilience for low-end phones.
- **Place & event data quality** (F19, F20, F21) — Crowd-correct the read-time view of stale OSM data via ingest-safe quorum overlays — venue facts, names/addresses, and event freshness.
- **Privacy & data-dignity as product** (F10, F11, F33, F34, F35, F36, F37, F43) — Make the platform's privacy and DSA/GDPR posture felt and reachable — appeals, self-audit views, data export, plain-language notices, and staff moderation tooling.
- **Nonprofit sustainability & civic impact** (F13, F23, F24, F25, F42) — Strengthen the donation-only funding base and civic legibility — recurring gifts, fiscal receipts, partner intake, and a volunteering category with an impact page.

## Candidates

### F1 — Quorum-go: a friendly minimum-to-happen threshold  `[S/imp4/low/keep]`
*Theme: Closing the find→commit loop*

**Pitch.** Let an organiser set "this runs if at least N RSVP going" so a wobbly maybe becomes a confident "we're on". The moment GOING first crosses the minimum, everyone is notified once; until then the panel honestly shows "needs N more".

**Why it fits the invariants.** Cohort-isolated, peer-only (a supervisory guardian can't trip it), blocked-pairs excluded, no PII/location/public surface. A per-activity count, never a per-user reliability rollup. Extends the existing RSVP/open_positions loop rather than adding a sidecar.

**Sketch.** Add owner-curated Activity.min_to_go + one-shot go_confirmed_at via the verbatim 4-point edit path (model field + ACTIVITY_EDITABLE_FIELDS + forms + serializers + migration). attendance_summary gains derived met_minimum/remaining_needed mirroring open_positions. Make set_attendance_intent @transaction.atomic; when GOING first crosses min_to_go, latch go_confirmed_at and fan out a new mutable Kind MEETUP_CONFIRMED to current members minus blocked pairs on transaction.on_commit (copying post_announcement). CRITICAL: go_confirmed_at latches ONLY the one-shot notification; the displayed chip stays a LIVE derived state ("N going — confirmed" / "needs N more") — never a latched-forever "confirmed" that lies after the count drops below min.

**Reuses:** F-RSVP attendance_intent/set_attendance_intent/attendance_summary; ACTIVITY_EDITABLE_FIELDS 4-point edit path; notifications mutable Kind + WHY_REASONS + no-op makemigrations; voting_members peer-only count + blocked_user_ids fan-out exclusion; transaction.atomic on set_attendance_intent + notify on on_commit
**Touches:** apps/social/models.py Activity (min_to_go + go_confirmed_at); apps/social/services.py (ACTIVITY_EDITABLE_FIELDS, set_attendance_intent hook + atomicity, attendance_summary); apps/social/serializers.py; apps/web/forms.py + apps/web/views.py activity_detail + template RSVP panel; apps/notifications/models.py (MEETUP_CONFIRMED Kind + WHY_REASONS) + no-op migration

### F2 — Map + nearby place picker for activity creation  `[M/imp4/low/keep]`
*Theme: Closing the find→commit loop*

**Pitch.** Replace the create form's flat alphabetical dropdown of every ingested venue with a type-first, request-only-proximity-ranked "pick the nearest courts that actually host this" picker, so organising a basketball game starts from a real court near you instead of scrolling a global list.

**Why it fits the invariants.** Read-only; coordinates stay request-only via parse_point/apply_proximity and are never stored. No photo/feed/tracking surface; place selection is cohort-agnostic while create_activity still pins cohort from the owner. The venue-near-a-point data is already AllowAny-public via NearMeView, so zero new exposure.

**Sketch.** Add read-only suggest_places(activity_type, near_lon, near_lat) wrapping the existing discovery query (public_places filtered by edge + is_disputed=False, .distinct()) then apply_proximity for request-only ordering; rank PlaceActivity origin CONFIRMED-before-INFERRED then distance (no invented confidence scalar). recently_used_places(owner) reads only the organiser's own past Activity.place set. The web activity_create swaps the place display into a type-first picker + reused _near_me.html Leaflet partial, seeding initial['place'] (the F40 path). CRITICAL: keep ActivityForm.place's queryset the FULL Place set so server-side validation still accepts any valid choice — the picker narrows display only. JS-free fallback = a shorter type-filtered <select>.

**Reuses:** PlaceActivity edges + is_disputed + origin INFERRED/CONFIRMED; public_places() visibility chokepoint; apply_proximity/parse_point request-only coords; F40 create-form prefill seeding initial['place']; F16 JS-free filtered-select fallback + _near_me.html partial
**Touches:** apps/web/forms.py ActivityForm (narrow display only, keep full-queryset validation); apps/web/views.py activity_create; apps/social/services.py (suggest_places + recently_used_places); apps/web/templates/web/activity_form.html

### F3 — Saved-search "tell me when a matching meetup appears" alert  `[M/imp4/low/revise]`  ✅ SHIPPED 2026-06-07 (126b2e5)
*Theme: Closing the find→commit loop*

**Pitch.** Let a user save one discovery filter (activity type/category + optional Area, within their own cohort) and get a single opt-in in-app notice the first time a new matching activity is created — so good meetups stop going unjoined while people re-run feeds. AREA-ONLY: no stored user coordinate.

**Why it fits the invariants.** Matching fans out strictly per-saver through visible_activities/can_see_activity (same-cohort only, blocked pairs excluded), so a saver is only told about an activity they could already see. No adult↔minor path, no public/photo surface, no cross-cohort leak. One mutable in-app notice with a "why you got this" line, no email/push. Search/save-only like Connections — no suggestions feed.

**Sketch.** New SavedSearch model (user FK; cohort PINNED from user.cohort at create; activity_type OR category; optional Area FK; optional beginners/cost-band filters; per-user cap; last_matched_cursor). NO coordinate field — Area is the only geo scope. A match_saved_searches command (appended to ops DUE_JOBS + its membership test) scans Activity.created_at__gt=cursor bounded by existing indexes, routes EVERY candidate through visible_activities(saver)/can_see_activity before firing ONE idempotent notify(ACTIVITY_MATCH) per (user, activity). Strict guardrails: opt-in, one-notice-per-pair ever, no digest cadence, no "N near you" counters, rate-limited, mutable. Web save control + /saved-searches/ CRUD + DRF viewset with serializer-allowlist test.

**Reuses:** visible_activities/can_see_activity per-saver read gate; ops run_due_jobs DUE_JOBS + membership test; notifications notify() choke point + mutable ACTIVITY_MATCH Kind + WHY_REASONS + makemigrations; Activity.created_at + (cohort,activity_type)/(cohort,place) indexes; communities.Area + Place.address_city (only geo scope)
**Touches:** apps/discovery (or apps/recommendations) — SavedSearch model + matcher (no coordinate field); apps/ops/management/commands — match_saved_searches + DUE_JOBS entry + test; apps/notifications/models.py — ACTIVITY_MATCH Kind + WHY_REASONS + makemigrations; apps/web — save control + /saved-searches/ + DRF viewset + allowlist test

### F4 — Recurring activity series (templated next-instance respawn)  `[M/imp4/med/keep]`  ✅ SHIPPED 2026-06-07 (3dd0e0c)
*Theme: Filling every seat fairly*

**Pitch.** Let a volunteer organiser define a repeating meetup once (e.g. every Tuesday 18:00 run) so the platform auto-spawns ONLY the next single Activity through the existing create path — sparing organisers weekly re-creation and keeping the standing meetup discoverable between instances.

**Why it fits the invariants.** No new write path (reuses create_activity so all cohort/consent/blocking gates hold). Each instance requires fresh per-member join (fresh consent for minors), so it never becomes a persistent roster or attendance rollup. No public feed/photo surface, no tracking. The cohort-isolation boundary is preserved by pinning series.cohort immutably and re-asserting it at spawn.

**Sketch.** New ActivitySeries (owner + place + activity_type + cohort + cadence + logistics template; place/type/cohort IMMUTABLE) + nullable Activity.series FK (SET_NULL). A spawn_due_series command (in ops DUE_JOBS) calls spawn_series_instance(), a thin wrapper over create_activity, materialising ONLY the next instance (plain date math, no rrule). CRITICAL enforced edges: (1) catch create_activity's NotEligible per-series and pause-not-abort so one broken series never kills the tick; (2) assert series.cohort == owner.cohort at spawn and pause on mismatch (a TEEN aging to ADULT must not silently spawn a minor series into the wrong cohort). Prior-member nudge reuses EVENT_REMINDER (no new Kind) and MUST exclude blocked pairs (no request user in cron). Audit each spawn/pause. Owner pause/end controls.

**Reuses:** ActivitySeries model + Activity.series FK + migration; create_series + spawn_series_instance wrapping create_activity (catch NotEligible→pause, assert cohort→pause, audit); spawn_due_series command (next-only, per-series failure isolation); ops run_due_jobs DUE_JOBS append; notifications EVENT_REMINDER (reused) through blocked_user_ids; social.Group — Group = standing space, Series = scheduled respawn (complementary)
**Touches:** apps/social/models.py; apps/social/services.py; apps/social/management/commands/spawn_due_series.py; apps/ops/management/commands/run_due_jobs.py; apps/web/views.py + templates; apps/social/serializers.py + apps/social/views.py

### F5 — Geography-aware, distance-bounded recommendations  `[M/imp4/low/revise]`  ✅ SHIPPED 2026-06-08 (dc921f1)
*Theme: Accessibility & inclusion*

**Pitch.** When a user opts into proximity on the home feed, fold a deterministic distance-decay into the recommendation ranking (and a soft access-match boost) so a child, wheelchair user, or elderly user who can't travel far isn't shown an unreachable "perfect match" alongside a nearby one — from request-only coords + declared tokens, never stored.

**Why it fits the invariants.** Coordinates stay request-only/transient via parse_point (never stored); inputs are declared/structural only (no behavioural tracking); recommend_activities already routes through cohort-gated visible_activities so isolation holds; no new surface, no photos/feed; the access signal stays a SOFT boost that never hides unknown-accessibility venues (F15 rule).

**Sketch.** SHIP THE CORE ONLY. (1) home() passes the parsed point + radius into recommend_activities (which already accepts near_point/radius_m and hard-filters by radius). (2) Inside recommend_activities, the pgvector path truncates in SQL on cosine distance BEFORE Python runs — so OVER-FETCH (3-4× limit) then re-rank in Python with score = cosine_sim × distance_decay(geo_m); decay stays a multiplier, never a filter beyond the existing radius (a great-but-distant match is de-prioritised, not erased). (3) small additive boost when matches_access_preference == match. (4) extend the rec_reason loop with a truthful "· near you" / "· step-free venue" suffix. With no coords, behaviour is byte-identical to today. SCOPE OUT to a separate candidate: the embeddings hash_embed area:/tod: token enrichment + any stored "preferred area/time" user vector — it dilutes the 64-dim space, forces a backfill, and a stored preferred area drifts toward a location proxy.

**Reuses:** recommendations.services.recommend_activities (over-fetch + Python re-rank; already accepts near_point/radius_m + hard-filters radius); discovery.proximity.parse_point (request-only transient); places matches_access_preference + accessibility_facts + get_access_preference (soft, never filters); web home (pass coords + radius, extend rec_reason suffix)
**Touches:** apps/recommendations/services.py; apps/discovery/proximity.py; apps/web/views.py; apps/places/services.py; apps/recommendations/tests

### F6 — Stale-age-proof safety sweep for minors (re-verify-or-pause)  `[M/imp4/low/keep]`
*Theme: Deepening the child-safety promise*

**Pitch.** A nightly ops sweep that proactively pauses participation and nudges re-verification when a minor's age proof is about to lapse, and cleanly evicts an already-lapsed minor from cohort-pinned rosters instead of letting them linger until they next act.

**Why it fits the invariants.** Turns expiry enforcement from lazy/reactive into active. Reads only band + expiry timestamps, never DOB. The guardian nudge keys strictly on an ACTIVE GuardianRelationship (no new adult-minor path). The notice is SYSTEM (non-mutable, DSA-consistent). No feed/photo/tracking surface, no behavioural rollup.

**Sketch.** The gap is real: apply_assurance evicts ONLY on a cohort CHANGE, is_assurance_current is lazy, and EUDI proofs carry an exp — so a stale-proof minor keeps is_identity_verified=True and lingers in rosters until they next act. New reverify_sweep command (in ops DUE_JOBS), bounded query over latest-per-minor AgeAssurance. EXPIRING-SOON (within REVERIFY_REMINDER_DAYS=14): SYSTEM notify to the minor + parallel SYSTEM notify to each ACTIVE-GuardianRelationship guardian (clone the arrival-ping loop). ALREADY-EXPIRED: call remove_user_from_groups + remove_user_from_conversations on the plain lapse + a SYSTEM "paused — please re-verify" notice. Eviction is naturally idempotent. The NUDGE needs an explicit sent-marker/window guard (the one genuinely-new state) so a nightly tick doesn't re-notify. Cap the eviction batch + audit against a mass-expiry/clock-skew event evicting a whole cohort in one tick.

**Reuses:** accounts is_assurance_current, assurance_provenance/days_left, REVERIFY_REMINDER_DAYS; accounts.identity.providers.eudi ASSURANCE_VALIDITY_DAYS; social remove_user_from_groups + ACTIVE-GuardianRelationship fan-out idiom; messaging remove_user_from_conversations; notifications SYSTEM (non-mutable); ops run_due_jobs DUE_JOBS; safety record_audit; NEW: sent-marker/window guard for the nudge
**Touches:** apps/ops/management/commands/run_due_jobs.py; apps/accounts/management/commands/reverify_sweep.py (new); apps/accounts/services.py; apps/notifications/services.py; apps/social/services.py; apps/messaging/services.py; apps/accounts/tests (new command tests)

### F7 — Guardian-set participation guardrails  `[M/imp4/med/keep]`
*Theme: Deepening the child-safety promise*

**Pitch.** Let a guardian convert all-or-nothing consent into a few conservative, child-read-only limits — adult-supervised meetups only, a latest-start hour, a daily/open-join cap — that the can_join gate enforces fail-closed across all active guardians.

**Why it fits the invariants.** It only ever NARROWS a minor's access, keys strictly on ACTIVE GuardianRelationship, adds no PII (boolean/int flags, not DOB or location), creates no adult-to-minor contact path, and renders honestly into the F13 guardianship_capabilities allowlist so legibility can never drift from enforcement. Stays legibility-only on the child side.

**Sketch.** New GuardianGuardrail(guardian, ward, supervised_only:bool, latest_start_hour:int|null, max_open_joins:int|null) keyed + gated on an ACTIVE GuardianRelationship with a CHILD ward; mutated only from wards() and audited inside the transaction. Extend can_join (the SINGLE join gate) to consult the STRICTEST active guardrail across ALL the ward's guardians, fail-closed. Three flags map to honest existing facts: supervised_only→guardian_accompanied; latest_start_hour→starts_at; max_open_joins→count of non-removed OPEN memberships. The guardians-of-ward query already exists (arrival-ping fan-out) — reuse it. Add the three flags to guardianship_capabilities so the F13 panel renders exactly what enforcement does. DROP the proposed public_venue_only flag: no honest public-space-vs-private-business signal exists in places, so it would make the legibility panel lie. Defer until a trustworthy venue-character signal exists.

**Reuses:** Real value only once minor onboarding is enabled (ALLOW_MINOR_ONBOARDING) with an ACTIVE GuardianRelationship; public_venue_only MUST be dropped (no honest backing fact); can_join + guardianship_capabilities + F13 panels + existing active-guardians-of-ward query + record_audit
**Touches:** apps/accounts/services.py (GuardianGuardrail model + helpers + guardianship_capabilities extension); apps/accounts/migrations; apps/social/services.py can_join (strictest active guardrail, fail-closed); apps/web/views.py wards() (audited edit) + my_guardians() (read-only legibility row)

### F8 — Kid-trusted one-tap "I feel unsafe" on the safe-exit card  `[S/imp4/med/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** Turn the safe-exit card into a one-tap, low-literacy "I feel unsafe" button that files a real moderation Report AND (for a child) sends each ACTIVE guardian a non-mutable SYSTEM alert with fixed server copy — so a scared kid mid-meetup reaches a real adult without navigating a reason-code form.

**Why it fits the invariants.** The guardian fan-out keys on ACTIVE GuardianRelationship, excludes blocked pairs, fires only for Cohort.CHILD, and uses FIXED server-composed copy with zero child-authored free text — so it never becomes a minor→adult text channel and leaks no PII. No new surface, no photo/feed, no tracking; reuses existing audited service primitives.

**Sketch.** A one-click POST web view on the safe-exit card (already member-not-owner, already has my_guardians). The view: (1) calls allow_action(user, "unsafe_report") — the web report path is currently UNTHROTTLED and file_report has no internal rate-limit, so the throttle + per-(reporter,activity) idempotency MUST be added here; (2) calls file_report(reporter=child, target=activity, reason=OFF_PLATFORM, detail="") — already atomic, audits, sends the Art.16 ack; (3) for a CHILD reporter, copies the mark_arrived idiom (loop ACTIVE GuardianRelationship, exclude blocked) but sends the guardian ping via Notification.Kind.SYSTEM — NOT a mutable kind and NOT ARRIVAL — because only MODERATION/SYSTEM are non-mutable; a safety alert a guardian could mute is a real defeat. _safe redirect back. The detailed reason-code form stays the slow path. No model/migration.

**Reuses:** safety.file_report (atomic, audit, Art.16 ack); safety.allow_action (MUST be added — file_report has no internal throttle); mark_arrived guardian fan-out idiom (ACTIVE GuardianRelationship, blocked exclusion, CHILD gate); accounts GuardianRelationship.ACTIVE + Cohort.CHILD; notifications SYSTEM (non-mutable, required) — no new kind; web _safe_next + activity_detail my_guardians context; safety ReasonCode.OFF_PLATFORM
**Touches:** apps/web/views.py (one-tap unsafe-report POST + URL + throttle + idempotency); apps/web/templates/web/activity_detail.html safe-exit card; apps/safety/services.py (thin CHILD guardian-fanout wrapper around file_report); apps/notifications/services.py notify() (SYSTEM kind); tests in apps/web + apps/safety

### F9 — Public meetup-place gate for children's activities  `[L/imp4/med/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** For CHILD-cohort activities, require the meetup Place to be a known public venue from a STAFF-curated venue-class allowlist (library, park, sports centre, school, community centre) — not "any non-USER place" — so a child meetup can't be set at an unverified or effectively-unsupervised location.

**Why it fits the invariants.** Closes a genuinely open gap: today create_activity/can_join place NO venue constraint on CHILD activities and guardian_accompanied defaults False. SAFETY.md already lists "public-place defaults" as backlog, so this builds on a stated-but-unbuilt safety item. No new PII, no Place field, no public feed/photo surface, no adult↔minor contact path. Read-time derivation matches the accessibility_facts pattern so re-ingest never clobbers it.

**Sketch.** Add a STAFF-curated venue-class allowlist as its OWN small table (do NOT reuse ingestion GENERIC_VENUES — it omits library, is OSM-only-shaped, and is a low-confidence candidate emitter, wrong semantics for a safety gate). A read-time public_child_venue_class(place) maps the venue via a per-source resolver (OSM MAPPING/GENERIC_VENUES, Overture categories.primary, Google category shape) → {allowed, not_allowed, unknown}. CRITICAL: (1) unknown-class is treated as NOT allowed (fail-closed) but the UI must say "this venue isn't on the approved list yet" with a staff-approval path, NOT silently 404/over-block legitimate non-OSM libraries; (2) drop the "supervised" claim — source!=USER + allowlist proves "known public venue type", not staffed supervision. Gate create_activity + can_join for Cohort.CHILD only, behind CHILD_PUBLIC_VENUES_ONLY (default ON). Cohort-visible chip via the _activity_card tag seam.

**Reuses:** NEW staff-curated venue-class allowlist (NOT GENERIC_VENUES); Per-source venue-class resolver (OSM/Overture/Google — raw_tags differs by source); create_activity + can_join CHILD gate chokepoints; read-time fact (accessibility_facts pattern); _activity_card chip seam + unknown-class staff-approval surface; NEW CHILD_PUBLIC_VENUES_ONLY flag (default True); fail-closed-but-not-silently-over-blocking unknown-class policy
**Touches:** apps/places/services.py; apps/social/services.py; apps/web/views.py + _activity_card.html; apps/ingestion/mapping.py + apps/ingestion/sources/overture.py; config/settings/base.py; apps/social/tests + apps/places/tests

### F10 — Your moderation appeal (finish the Art.17/20 stub)  `[M/imp4/low/revise]`
*Theme: Privacy & data-dignity as product*

**Pitch.** Let a moderated user actually contest a decision — and make the appeal reachable by the suspended/banned users who most need it — closing the DSA Art.20 loop the UI promises in three places but cannot deliver.

**Why it fits the invariants.** Pure data-dignity and EU-compliance work with no new contact surface: strictly self-scoped, no cross-user data, no minor-exposure path, no feed/photo/tracking surface. It finishes a broken promise (safety_record.html, the Art.17 SoR, terms.html) and clears an explicit pre-launch item (COMPLIANCE_CHECKLIST L-DSA-20). CHILD/TEEN appealing their own account routes to staff, never adults.

**Sketch.** Add safety.Appeal (FK to ModerationAction, free-text grounds, status, partial-unique constraint = at most one OPEN appeal per action). file_appeal is atomic and MUST re-derive ownership exactly as safety_record_for does (rebuild action_q: user-target OR own-activity OR own-post) and fetch under that filter — never trust the posted id, or it becomes an IDOR oracle. Rate-limit via allow_action. On resolve, fire a NON-MUTABLE MODERATION notice (reuse _notify_statement_of_reasons wiring; no new Kind). safety_record_for adds ModerationAction.id + appeal_status to each decision dict (the id identifies the viewer's OWN action). RESHAPE — the load-bearing fact: take_action sets is_active=False on SUSPEND/BAN and safety_record is @login_required, so the suspended population Art.20 most protects can't reach the button. The deliverable is NOT done until the suspended-user access path is designed (logged-out token-gated route, or documented out-of-band channel) with legal.

**Reuses:** safety_record_for adds ModerationAction.id + appeal_status (self-scope re-checked inside file_appeal); safety Appeal model + migration (partial-unique: one OPEN appeal per action); file_appeal + resolve_appeal reusing allow_action, record_audit, _notify_statement_of_reasons NON-MUTABLE MODERATION (no new Kind); safety admin staff resolution action; web safety_record + template (Contest this form); align terms.html; ACCESS PATH (design + legal): route reachable by a SUSPENDED user
**Touches:** apps/safety/models.py; apps/safety/services.py; apps/safety/admin.py; apps/safety/migrations (new); apps/web/views.py; apps/web/templates/web/safety_record.html + terms.html

### F11 — Moderation triage hints for the report queue (staff-only)  `[M/imp4/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** Add deterministic, advisory priority signals (reason severity, CHILD-cohort involvement, count of open reports against the same target, optional contact-detail/off-platform keyword hits) to the EXISTING staff-only report queue, so a tiny moderation team works the most dangerous reports first.

**Why it fits the invariants.** Staff-only, read-only, audited on access, takes no automated action — the human decision (take_action) is unchanged. It ranks open reports, not people; signals are computed live with no per-user rollup, so it can't become a profiling/reliability/vanity surface. No new PII (CHILD is a derived boolean, never age band/DOB), no public/photo surface, no adult↔minor path, no external dep. DSA-aligned tooling.

**Sketch.** Add safety.triage_summary(report) computing ordering signals from existing data: ReasonCode severity rank (GROOMING/CSAM top), whether _affected_user(target).cohort == CHILD, a live count of OPEN Reports sharing (target_type,target_id) (the existing index covers this), and — as the LOWEST-WEIGHT advisory signal — a fresh deterministic RO/EN keyword scan of a reported Post body. CORRECTION: there is NO existing pre-send-nudge ruleset to reuse (BasicMessagePolicy only trims/length-caps); the phrase lists + tests are net-new and must NEVER be the sole sort key. Prefer EXTENDING the IsModerator-gated DRF ModerationReportListView ordering over a brand-new web template. Audit access. Pin tests: no per-user rollup persisted, signal set never user-facing, CHILD detection never leaks age band/DOB.

**Reuses:** safety ReasonCode, Report (status,reason)/(target_type,target_id) indexes, ModerationAction, _affected_user, record_audit, existing IsModerator DRF queue; accounts User.cohort/Cohort.CHILD + IsModerator; NEW deterministic RO/EN contact-detail keyword ruleset + tests (does NOT exist today)
**Touches:** apps/safety/services.py (triage_summary); apps/safety/views.py (extend ModerationReportListView ordering); apps/web/views.py + template (OPTIONAL staff page); NEW RO/EN keyword ruleset module + tests

### F12 — Display preferences: dark/high-contrast theme, larger text, reduced motion  `[S/imp4/low/keep]`
*Theme: Accessibility & inclusion*

**Pitch.** A no-JS-required display panel (dark / high-contrast theme, larger text, reduced motion) that honours OS preferences by default and persists via a functional cookie — making the single fixed light UI usable for low-vision, photosensitive, elderly, and vestibular users across every page.

**Why it fits the invariants.** Purely presentational: touches no cohort isolation, child-contact, PII, photo-feed, or engagement surface. Strengthens the EU accessibility/civic-adoption story; adds no tracking/ads/vanity metrics. The cookie is strictly functional (no consent banner needed); honouring prefers-color-scheme/prefers-reduced-motion as default is privacy-respecting. No new deps, no JS dependency for core behaviour.

**Sketch.** base.css is already CSS-custom-property driven (:root with --bg/--fg/etc.), so add [data-theme=dark]/[data-theme=contrast] override blocks + a --scale font multiplier — additive, no refactor. Reference implementations of the data-theme + prefers-color-scheme + prefers-reduced-motion pattern already exist in static/mockups/. base.html (the single base extended by 40 templates) sets data-theme/style=--scale on <html> + an @media reduced-motion block. Source of truth = a functional cookie set by a tiny progressive-enhancement toggle; fall back to OS prefs when unset. community-graph.js ALREADY gates animation on prefers-reduced-motion (done). The logged-out cookie value reaches base.html via a tiny new context processor (none exists; _nav_context returns {} for anon). RESHAPE: make the per-user model OPTIONAL/deferred — a functional cookie alone serves everyone for a cosmetic toggle. Main real cost is cosmetic: theming every component + verifying --scale across the Leaflet map, chat thread, cards.

**Reuses:** base.css custom-property :root tokens (in place); base.html single base template (40 templates); static/mockups reference implementations; community-graph.js reduced-motion gate (already done); CSRF/session/cookie middleware (present); NEW tiny context processor (none exists; _nav_context returns {} for anon); AccessPreference lives in apps/places (mirror IF a DisplayPreference is later added)
**Touches:** static/css/base.css; templates/base.html; apps/web/views.py (settings view + context processor); config/settings/base.py (register context processor)

### F13 — Volunteering activity category + civic impact dashboard  `[M/imp4/low/keep]`
*Theme: Nonprofit sustainability & civic impact*

**Pitch.** Add a "civic/volunteering" branch to the taxonomy (park clean-ups, library shelving, community-garden tending) so the find→join→show-up loop produces measurable real-world good, plus a privacy-safe, cohort-blind, small-cell-suppressed public /impact page that shows that good to funders and the city.

**Why it fits the invariants.** Volunteering is a high-civic-value activity type and a public /impact page is a credible donor-acquisition lever. Text-only, no per-user rows, no "X of Y" bar. Child safety is preserved structurally because the Communities k-anon floor is inherited with zero code change, so a "Volunteering" label can never surface to a child cohort off too-few real participants.

**Sketch.** Seed migration adds a civic/volunteering ActivityCategory + types (park_cleanup, library_volunteering, community_garden). classify_activity and recommendations.embeddings extend automatically (derive from slug/name/aliases + category ancestry — verified). Communities auto-materialises a per-cohort "<City> Volunteering" label behind the existing k-anon floor (no code change, child-safe by construction). Ingestion needs a SMALL real change (NOT free): add GENERIC_VENUES entries for new OSM tags (social_facility, leisure=garden/community_garden) — confirmed ABSENT today. Then an aggregate_impact() service modeled on spend_by_category: COMPLETED activity count, COUNT(DISTINCT place), public-Partner count, per-category counts — MANDATORY small-cell suppression, explicitly COHORT-BLIND and place/time-blind so a single-city launch can't leak where minors gather. Public /impact server-rendered page reuses transparency.html + |cents (no DRF AllowAny endpoint).

**Reuses:** taxonomy (new ActivityCategory + ActivityType seed migration); ingestion/mapping.py (NEW GENERIC_VENUES entries — confirmed absent, NOT free); events/classify.py (free keyword extension — verified); recommendations/embeddings.py (free token extension — verified); communities (auto-materialises behind k-anon floor — no code change); NEW aggregate_impact() with mandatory small-cell suppression, cohort-blind + place/time-blind; web (/impact view + template reusing transparency.html + |cents)
**Touches:** apps/taxonomy/migrations/0006_seed_civic_volunteering.py; apps/ingestion/mapping.py; apps/ops/services.py (or apps/donations/services.py) aggregate_impact(); apps/web/views.py + urls.py + impact.html; apps/communities/tests + apps/ops/tests (k-anon + small-cell tests)

### F14 — Plan-B fallback meetup point on cancel  `[S/imp3/low/keep]`
*Theme: Filling every seat fairly*

**Pitch.** Let an organiser cancel-with-redirect: instead of a dead-end cancellation, the same members get a one-tap link to join a designated replacement meetup at the SAME place/type/cohort — each still passing the normal join gates, so a rained-out run that just moves doesn't evaporate the group.

**Why it fits the invariants.** No auto-roster-migration (each member re-passes can_join/consent/cohort/capacity). Reuses the structural place/type/cohort immutability so no bait-and-switch is possible. No PII, no tracking, no new contact surface. The deep-link lands on a same-cohort join page, so a guardian or child following it stays inside the identical supervisory/cohort context. No public feed/photo surface.

**Sketch.** Add an optional replacement_activity_id to cancel_activity. Before flipping status, validate the target is OPEN and shares place_id + activity_type_id + cohort with the cancelled activity (reusing the locked-field invariants); else raise InvalidState. The existing ACTIVITY_CANCELLED fan-out carries body text + a deep-link to the replacement's join page. CRITICAL FIX: that fan-out currently does NOT exclude blocked pairs (unlike post_announcement) — adding a replacement link without a blocked_user_ids filter would surface a blocked owner's new activity to a member who blocked them, so this MUST add the blocked filter to the cancel fan-out (helper already imported). A small web cancel-screen control. No new Notification.Kind. Enforce the replacement is OPEN at fire time so a stale target can't produce a dead link.

**Reuses:** cancel_activity (replacement validation + deep-link in existing fan-out); can_join + ACTIVITY_EDITABLE_FIELDS immutability (same place/type/cohort check); safety blocked_user_ids (MUST be added to the cancel fan-out); web activity cancel view + template; notifications ACTIVITY_CANCELLED (reused, no new kind)
**Touches:** apps/social/services.py; apps/web/views.py; apps/web/templates (activity cancel)

### F15 — "You're going — here's how to get there" pre-meetup brief  `[M/imp3/low/revise]`
*Theme: Actually showing up*

**Pitch.** Enrich the EVENT_REMINDER members already get with the activity's meeting point, what-to-bring, and (when available) a venue maps link — so committed people arrive at the right spot, on time, prepared.

**Why it fits the invariants.** Reminders go only to existing confirmed members where cohort isolation already holds, so no adult-to-minor path. attendance_intent stays read-only and is never aggregated. maps_uri is read at send-time from raw_tags, never written to Activity (read-time-derivation pattern that survives re-ingest). No new PII, no photo/feed surface.

**Sketch.** ENRICH the existing single reminder body, do NOT build a second pass. send_activity_reminders already does ONE global pass over MEMBER with idempotent (recipient, EVENT_REMINDER, url) dedup, respects notify()'s mute gate, fanned out by run_due_jobs. Compose the body deterministically from the member-gated meeting_point/what_to_bring + place.raw_tags['google']['maps_uri'] when present (gracefully omit the maps line when GOOGLE_PLACES_API_KEY was never run — most OSM-only Cluj places). RESHAPE: ship the enriched single-reminder brief to ALL current members now. DROP the GOING filter entirely (it would regress coverage — default intent is UNKNOWN and guardians never RSVP). DEFER per-user reminder_lead_hours to its own scoped change — a second short-lead pass COLLIDES with the single (recipient,kind,url) dedup and _supersede_reminders.

**Reuses:** notifications: enrich send_activity_reminders body (no new field if lead-time deferred); social: read-only meeting_point/what_to_bring; attendance_intent untouched (do NOT add GOING gate); places: read-time raw_tags['google']['maps_uri'] helper (absent without GOOGLE_PLACES_API_KEY); ops run_due_jobs already schedules send_activity_reminders
**Touches:** apps/notifications/management/commands/send_activity_reminders.py; apps/places/services.py (maps_uri read helper); apps/notifications/tests/test_reminders.py (update body assertions)

### F16 — Heads-up "running late" day-of status  `[S/imp3/low/revise]`
*Theme: Actually showing up*

**Pitch.** A member who already RSVP'd can flip one transient, no-free-text "running late" flag inside the arrival window that quietly pings the group so nobody reads a latecomer as flaking — no shame, no history, no location.

**Why it fits the invariants.** Server-composed fixed copy (no free text), no location, no DOB/PII, transient + reset-on-leave + auto-cleared by expire_arrivals (never a presence/reliability record), blocked pairs excluded, member + can_participate gated, window/OPEN gated. CHILD guardian fan-out keys on the ACTIVE GuardianRelationship exactly like mark_arrived. Notification kind stays mutable.

**Sketch.** Add one null-defaulted Membership.day_of_status (NONE/RUNNING_LATE) mirroring arrived_at + a no-op migration. set_day_of_status is a near-clone of mark_arrived: reuse arrival_window_open verbatim, the same current_members/can_participate/blocked_user_ids/_notify/record_audit seams, idempotent. Fan out fixed copy ("X is running late to …") to other current members minus blocked pairs, plus CHILD active-guardian(s). Clear it in the same expire_arrivals bulk update + the leave-reset block. DELIBERATELY do NOT add a CANT_TODAY value: "can't make it today" is already AttendanceIntent.NOT_GOING ("Can't make it"), which is not even window-gated, so a second flag would be a confusing redundant double-signal. Collapse to RUNNING_LATE only; keep it idempotent + rate-limited so it can't become a poke channel.

**Reuses:** arrival ping (Membership.arrived_at, mark_arrived, arrival_window_open, expire_arrivals in DUE_JOBS); RSVP intent (attendance_intent transient + reset-on-leave) — its NOT_GOING subsumes 'can't make it today'; notifications notify() + new mutable Kind + WHY_REASONS + no-op makemigrations; safety blocked_user_ids + record_audit; web activity_detail
**Touches:** apps/social/models.py (Membership.day_of_status); apps/social/services.py (set_day_of_status, leave-reset block); apps/social/management/commands/expire_arrivals.py; apps/notifications (new mutable Kind + WHY_REASONS); apps/web (activity_detail button)

### F17 — Organizer logistics-readiness coach + pre-start nudge  `[S/imp3/low/keep]`
*Theme: Actually showing up*

**Pitch.** A deterministic, owner-only "is this meetup ready to run?" card that flags blank logistics fields (meeting point, what-to-bring, capacity, RSVP count), each gap deep-linking to the existing edit path — plus one idempotent pre-start nudge to the organiser if gaps remain.

**Why it fits the invariants.** Strictly self-facing to the owner about their own activity: no member-visible surface, so it creates no per-user reliability/quality/vanity metric. Writes nothing (pure read-time over existing fields), routes the one notice through notify() as a mutable kind behind the WHY_REASONS line. No PII, tracking, photo surface, or adult↔minor path.

**Sketch.** Add a pure read-time social.logistics_gaps(activity) over fields confirmed present on Activity (meeting_point, what_to_bring, cost_band, difficulty, accessibility_notes, capacity) plus attendance_summary(). Returns a deterministic ordered list, writes nothing. Render as an owner-only card in activity_detail (is_owner already in context, mirroring the digest/rsvp/safe-exit cards), each gap deep-linking to the update_activity edit path. Extend send_activity_reminders to send the OWNER one idempotent ORGANIZER_PREP notify (dedup on (recipient,kind,url)) when gaps remain; add a mutable Kind ORGANIZER_PREP + WHY_REASONS + no-op makemigrations. RESHAPE: DROP the per-category taxonomy hint table ("running→water?") from v1 — it is the only migration, the only per-type i18n burden, and the only piece drifting toward an engagement-prompt. Owner can be a CHILD, so card copy must be cohort-neutral and self-facing.

**Reuses:** Activity logistics fields + capacity + ACTIVITY_EDITABLE_FIELDS/update_activity edit path; attendance_summary() read snapshot; send_activity_reminders + (recipient,kind,url) idempotency (mind _supersede_reminders clears only EVENT_REMINDER); notify() + NON_MUTABLE/MUTABLE gate — new mutable ORGANIZER_PREP + no-op makemigrations; is_owner owner-card pattern in activity_detail
**Touches:** apps/social/services.py (logistics_gaps — read-only over existing fields + attendance_summary); apps/web activity_detail owner-only card + template; apps/notifications (ORGANIZER_PREP Kind + WHY_REASONS + extend send_activity_reminders); apps/notifications migration (no-op)

### F18 — Mirror meetup logistics to the child's guardian manifest (+ "getting home" note)  `[S/imp3/low/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** For CHILD meetups, mirror the owner-curated logistics a parent can't currently see — exact meeting spot, end time, and a new "getting home" note — onto the read-only /wards/ manifest, so a guardian sees the actual plan, not just the venue.

**Why it fits the invariants.** The guardian view is read-only facts with no reply channel (no adult↔minor contact path), keyed on the ACTIVE GuardianRelationship the wards query already uses; owner-curated short text only — no DOB, no stored location, no public/photo surface, no tracking. Text-first logistics for a real in-person meetup; no new deps.

**Sketch.** Do NOT add exact_meeting_spot or ends_by — they duplicate the shipped F9 meeting_point and the structured Activity.ends_at. The only net-new field is getting_home_note (TextField, blank), added to ACTIVITY_EDITABLE_FIELDS and capped at LOGISTICS_FIELD_MAX_LENGTH, routed through update_activity and shown in the member-gated logistics card. The headline deliverable is the guardian mirror: extend the wards() loop to surface, per upcoming MEMBER meetup, the already-stored meeting_point, ends_at, and getting_home_note in wards.html (which today shows only starts_at/type/place). Gate the mirror to CHILD-cohort wards (teens self-manage). draft_activity_text already branches on CHILD/TEEN — extend its safety-reminder seed to getting_home_note via setdefault so it never overwrites typed input.

**Reuses:** Activity.getting_home_note (one new field + no-op migration); ACTIVITY_EDITABLE_FIELDS + draft_activity_text CHILD/TEEN seed (setdefault); serializers + forms LOGISTICS_FIELD_MAX_LENGTH cap; wards() — surface existing meeting_point/ends_at + new note per CHILD ward; wards.html manifest mirror + activity_detail.html logistics card
**Touches:** apps/social/models.py; apps/social/services.py; apps/social/serializers.py; apps/web/forms.py; apps/web/views.py; apps/web/templates/web/wards.html + activity_detail.html

### F19 — Crowd venue facts + kid-suitability facts (ingest-safe overlay)  `[M/imp3/low/keep]`
*Theme: Place & event data quality*

**Pitch.** Let verified members confirm/dispute concrete, low-literacy VENUE facts OSM rarely records — drinking water, lit at night, indoor shelter, toilets, fenced/away-from-traffic, shade, playground — via the same ingest-safe quorum overlay that powers edge votes, surfaced OSM-first as honest neutral facts (never a composite "safe-for-kids" verdict).

**Why it fits the invariants.** A closed fact_key allowlist with yes/no values (no free text = no covert channel); counts-only display with no voter identity; a soft kid filter that never hides "unknown" (F15 rule); pure additive overlay that never writes Place/raw_tags so re-ingest can't clobber it. Facts describe the physical venue, not user presence — no adult↔minor path, no new minor-locating signal beyond public OSM tags, no PII.

**Sketch.** New PlaceFactVote overlay (place, user, fact_key from a FIXED closed allowlist, value yes/no, unique per (place,user,fact_key)) — a near-verbatim clone of ActivityEdgeVote, same quorum=3, ingest never touches it. place_fact_status derives a tristate per fact at read time. venue_facts reads existing OSM tags FIRST (leisure=playground, barrier=fence, toilets, natural=tree, drinking_water) via _tristate, then overlays crowd votes only where OSM is silent. vote_on_fact gates on can_participate + public_places + allow_action rate-limit + idempotency. Surfaces show counts + the viewer's own vote only; a ?kid_friendly=true SOFT filter never hides unknown. RESHAPE: (1) neutral individual facts, NOT a composite "good for kids" score; (2) state cohort eligibility explicitly (can_participate is not cohort-scoped — correct for objective facts but documented); (3) test that co-voting on a place is NOT a shared activity and never enables can_connect.

**Reuses:** ActivityEdgeVote/vote_on_edge quorum overlay + DEFAULT_EDGE_QUORUM + public_places chokepoint; accessibility_facts read-time OSM derivation + _tristate; OpenNowReport ingest-safe overlay + allow_action + idempotency + recent_report_n annotation; accounts.can_participate; safety.allow_action + record_audit; ingestion OSM tags present in raw_tags
**Touches:** apps/places/models.py (PlaceFactVote) + apps/places/facts.py; apps/places/services.py (place_fact_status, venue_facts); apps/places/migrations (one additive); apps/web place_detail + places_list + templates; apps/discovery NearMeView + serializers (?kid_friendly soft filter); apps/places/views.py PlaceViewSet (annotation); apps/places/tests

### F20 — Crowd-corrected venue name & address (quorum edit overlay)  `[M/imp3/low/keep]`
*Theme: Place & event data quality*

**Pitch.** Let members propose a corrected venue name/address behind the same N-confirmer quorum that already governs user-proposed places, surfaced read-time over stale OSM data (never written back) so meetup locations stop being confusingly labelled.

**Why it fits the invariants.** Place metadata is cohort-agnostic, creates no contact path, no roster, no feed. Counts-only pending UI (no proposer/confirmer identities, mirroring F25/F26). No PII beyond the FK pattern F25 already carries. No photo/public-feed surface. The read-time overlay (F28 pattern) never writes Place, so it can't poison canonical OSM and survives re-ingest — the gap staff-admin edits (clobbered on re-ingest) can't fill.

**Sketch.** Add a PlaceCorrection overlay (place FK, proposer FK, field in {name, address}, proposed_value, status, + confirmation join) cloning UserPlaceProposal/PlaceConfirmation verbatim: proposer excluded from confirming, DEFAULT_PLACE_QUORUM independent confirmers, can_participate-gated, staff fast-publish/reject + audit. Proposed value runs through the existing [:255]-strip sanitisation; only corrections on public_places() are eligible. CORRECTION: expose the overlay as Place.display_name/display_address PROPERTIES (prefer applied correction, else raw OSM), NOT free service functions — templates render the value LIVE ({{ a.place.name }}), so a model property reaches all 11 templates + serializers in ONE seam. KNOWN LIMIT (state honestly): the frozen Activity.name/slug composed at creation and the address_city used for community geo-binding will NOT retroactively reflect a correction — acceptable, but don't overstate reach. Pending UI shows counts only.

**Reuses:** F25 UserPlaceProposal quorum (confirm_place, DEFAULT_PLACE_QUORUM, staff_publish/reject); F28 ingest-safe read-time overlay pattern (never writing back to Place); public_places single visibility chokepoint; accounts.can_participate; safety record_audit (inside the txn); [:255]-strip text sanitisation
**Touches:** apps/places/models.py (PlaceCorrection + Place.display_name/display_address properties); apps/places/services.py (propose/confirm/staff-publish/reject + audit); apps/places/serializers.py + apps/discovery/serializers.py + apps/events + apps/social serializers (place_name source); apps/web/views.py place_detail + correction confirm view; apps/web/templates (switch {{ place.name }} → {{ place.display_name }}); apps/places/views.py PlaceViewSet

### F21 — Event accuracy reports (EventReport overlay)  `[M/imp3/low/revise]`
*Theme: Place & event data quality*

**Pitch.** Bring the shipped F28 "hours are wrong" overlay pattern to events — an ingest-safe, decaying member report (cancelled/moved/wrong-time) that flags or demotes stale events out of the Happening feed, so a one-way ingest no longer leaves dead events live.

**Why it fits the invariants.** Counts-only with no per-user reliability rollup. Read-time, decaying, ingest-safe overlay off the Event model (survives re-ingest). Gated on can_participate + rate-limited + idempotent, mirroring F28's anti-brigading. No photo/feed surface, no PII, no adult-minor contact path: the tally is a count, never reporter identity.

**Sketch.** Part 1 ONLY (Part 2 split out). Add events.EventReport(event FK, reporter FK, kind: cancelled/moved/wrong_time, created_at) — a DEDICATED overlay, NEVER a field on Event (upsert_event update_or_create would clobber it). Temporal uniqueness enforced in the service, like OpenNowReport. Read-time event_reliability(event) returns a "members reported this may have changed" sentinel once ≥3 recent reports land within the decay window; optionally auto-demotes below the Happening cutoff (annotate recent_report_n via Count+filter to avoid N+1). file_event_report clones file_open_now_report verbatim. EXPLICIT DECISION to document: events are AllowAny + NOT cohort-scoped and can_participate is cohort-blind, so a verified CHILD and ADULT report into the SAME event tally — acceptable because an event being cancelled is cohort-neutral physical reality and the tally is counts-only, but state it in the model docstring + a test. DEFER Part 2 (retrofitting a kind field onto the shipped F28 OpenNowReport) to its own candidate.

**Reuses:** F28 OpenNowReport pattern (model, open_now_status, file_open_now_report, clear, recent_report_n annotation, web report/reset views — all present); safety.allow_action; accounts.can_participate (note: cohort-blind); events Event + upsert_event idempotent re-ingest (overlay must survive update_or_create); discovery HappeningView + web event_detail (a real 16-line template, not a stub)
**Touches:** apps/events/models.py (EventReport); apps/events/services.py (event_reliability, file_event_report, clear_event_reports); apps/events/admin.py + migration; apps/discovery/views.py HappeningView (annotation + optional demotion); apps/web/views.py event_detail + report/reset handlers + urls + template; apps/events/tests

### F22 — Co-organizer seat + graceful ownership handoff  `[L/imp3/med/revise]`
*Theme: Filling every seat fairly*

**Pitch.** Let an owner grant a current same-cohort member co-organizer rights (post announcements, edit logistics, admit joiners) and cleanly hand off ownership, so a thriving group survives a volunteer leaving — and a GDPR erasure no longer silently CASCADE-destroys an evidence-bearing group thread.

**Why it fits the invariants.** An owner today literally cannot leave their own activity and erase_user CASCADE-destroys every owned Group + thread. A clean handoff protects moderation-evidence-bearing threads from destruction. No new PII, no tracking, no public/photo surface, no adult↔minor path (gate reuses voting_members which already excludes GUARDIAN and same-cohort).

**Sketch.** Add Membership.Role.CO_ORGANIZER + GroupMembership.Role.CO_ORGANIZER and an is_organizer(user, owner_obj) helper; rewire the 6 activity owner gates + 2 group gates through it, fail-closed. grant_co_organizer/revoke + transfer_ownership are atomic + audited, gated on a current same-cohort non-GUARDIAN MEMBER. CRITICAL CORRECTION: minor groups are is_staff_curated and require actor.is_staff; a same-cohort minor MEMBER is NEVER staff, so peer co-org/transfer is structurally impossible for minor activities/groups — restrict grant/transfer to ADULT cohorts (and staff→staff for minor groups only). DO NOT add a transfer pre-step to erase_user — keep it non-interactive and deterministic; let an owner transfer voluntarily BEFORE erasure and keep the existing audited CASCADE as fallback. Scope to Activities first; Groups ship dark at launch.

**Reuses:** Membership.Role + GroupMembership.Role CO_ORGANIZER (2x makemigrations); is_organizer helper + rewiring 6 activity + 2 group owner gates; grant_co_organizer/revoke + transfer_ownership (adult-cohort only; staff-preserving for minor groups); safety record_audit; web organizer panel + DRF organizer action
**Touches:** apps/social/models.py; apps/social/services.py (owner-gate rewiring + grant/revoke/transfer); apps/web/views.py (owner-only check + organizer panel); apps/social/views.py (DRF organizer action)

### F23 — Recurring-gift engine (real monthly support)  `[M/imp3/med/keep]`
*Theme: Nonprofit sustainability & civic impact*

**Pitch.** Turn the stored-but-dead Donation.recurring boolean into a real, EU-compliant, fully donor-cancellable monthly/quarterly giving option so the nonprofit gets predictable income — no card data stored, no nags or scarcity, idempotent reconciliation.

**Why it fits the invariants.** Predictable recurring income is the financial backbone of a donation-only nonprofit. Touches NO child-safety surface — donations are fully decoupled from cohorts/minors. Stores only an opaque external subscription id (no card data). Explicitly bans dark patterns (no scarcity/countdown/nag), consistent with the calm F29/F34 precedent.

**Sketch.** Add a StripeSubscriptionProvider with create_subscription using Checkout mode=subscription (needs a recurring Price, not the inline price_data the one-off path uses). Add a RecurringGift model holding ONLY the opaque subscription id + cadence + status + optional donor SET_NULL. The webhook currently acts only on checkout.session.completed; add an invoice.payment_succeeded branch. CRITICAL: complete_donation reconciles by PENDING external_ref and CANNOT be reused — each renewal carries a NEW invoice id, so renewals must INSERT a fresh COMPLETED Donation(recurring=True) keyed off the subscription id, made idempotent against Stripe's at-least-once retries via a unique constraint on the invoice id (else a double-write corrupts the public completed_total_cents figure). Add cancel_recurring (one-click, nag-free) on /my-donations/. Expose the recurring choice on the web DonateForm (today only the DRF serializer accepts it). Gate behind Stripe-provider activation.

**Reuses:** Stripe provider activated in prod (DONATIONS_PROVIDER, STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET) — INERT until flipped; Stripe SCA/PSD2-compliant recurring subscriptions (recurring Price/product + off-session mandate); Phase-2 legal/ToS/DPIA (recurring-gift terms, tax receipts, cancellation/refund policy); Web DonateForm must expose the recurring choice
**Touches:** apps/donations/providers.py (create_subscription); apps/donations/models.py + migration (RecurringGift, unique-invoice idempotency); apps/donations/services.py (renewal reconciliation + cancel_recurring); apps/donations/views.py + webhooks.py (invoice.payment_succeeded branch); apps/web/forms.py + views.py + templates

### F24 — EU fiscal donation receipt + transparency over time  `[S/imp3/low/keep]`
*Theme: Nonprofit sustainability & civic impact*

**Pitch.** Give each completed donor a self-service, printable GDPR-clean fiscal receipt (org legal details + opaque ref) and render the existing SpendEntry ledger as an honest period-over-period table, so gifts can be tax-deductible and donors see stewardship over time.

**Why it fits the invariants.** A valid fiscal receipt unblocks deductible and institutional giving, the org's sole revenue source. Strictly self-scoped (mirrors the my_donations donor=request.user gate), allowlisted fields only, aggregate-only spend section with no goal/target framing (preserves the F29/F34 no-X-of-Y stance), text-first server-rendered HTML with no chart library, zero child-safety surface.

**Sketch.** spend_by_period(currency) is a values('period','category').annotate(Sum) with NO schema change (SpendEntry.period is already a stored CharField). Add donation_receipt(donation, requester) that re-asserts donation.donor_id == requester.id and FAILS CLOSED for anonymous gifts (donor NULL) and non-COMPLETED status, returning an allowlisted dict + ORG_LEGAL_* from settings. REFINEMENT: route the URL on the donation pk (already self-scoped), NOT external_ref — for the real Stripe provider external_ref is the Checkout Session id, a provider-meaningful token. /my-donations/<pk>/receipt/ 404s otherwise. Render server-side printable HTML, reuse |cents. Add ORG_LEGAL_NAME/ORG_REG_NO/ORG_ADDRESS settings. Gate launch on real ORG_LEGAL_* values. Tests: strict donor==requester self-scope, fail-closed anonymous + non-COMPLETED.

**Reuses:** Real registered nonprofit legal details (ORG_LEGAL_*) — placeholders make the receipt fiscally invalid; Existing F29 stack (my_donations self-scope, spend_by_category aggregate, SpendEntry.period CharField, |cents); RO/EU fiscal-receipt format confirmation (overlaps the planned legal sign-off; non-blocking for code)
**Touches:** apps/donations/services.py (donation_receipt, spend_by_period); apps/web/views.py + urls.py (/my-donations/<pk>/receipt/); templates/web/donation_receipt.html + transparency.html; config/settings (ORG_LEGAL_* constants)

### F25 — In-kind + grant transparency and partner intake  `[M/imp3/med/revise]`
*Theme: Nonprofit sustainability & civic impact*

**Pitch.** Give libraries/schools/NGOs a self-serve, rate-limited /partners/apply/ front door to become a verified Partner, and make the platform's biggest non-cash lifeline (donated venue hours, volunteer hours) visible as its own aggregate-only /transparency section.

**Why it fits the invariants.** Partner venues are core to the "we already know the places" promise, yet today an institution has no in-app path to become one. Touches zero child-safety surface — only institutional/financial data. Partner stays text-only with no logo, public() remains the single visibility chokepoint, pending applications never auto-publish, and in-kind value is shown separately, never summed into a cash "X of Y" bar.

**Sketch.** RESHAPED to lead with the missing capability. (1) PartnerApplication model + a public, CSRF-protected, IP-rate-limited /partners/apply/ form creating a PENDING row; staff approval flips it into a verified Partner (mirrors the F25 UserPlaceProposal pending→PUBLISHED flow). Website sanitised via safe_external_url; blurb capped at 280; pending rows never render publicly. (2) InKindContribution model (FK to Partner, free-text category, integer quantity+unit, optional estimated_value_cents, period label) — mirroring SpendEntry's aggregate-only design, surfaced as a SEPARATE /transparency section, NEVER added into completed_total_cents. CATCH: allow_action keys strictly on user.id, so the anonymous public apply write needs a NEW IP-keyed throttle (the _client_ip helper exists but isn't wired into allow_action) — this anonymous write is the one genuinely novel attack surface. DEFER the SpendEntry.campaign FK + campaign_reconciliation to its own later candidate.

**Reuses:** F37 verified civic partners (Partner, public() chokepoint, verified_partners, /partners/) — shipped; F29 transparency (SpendEntry aggregate-only, two-section discipline, never-X-of-Y rule) — shipped; F25 UserPlaceProposal pending→PUBLISHED + staff-approval prior art; safety.sanitize.safe_external_url; web._client_ip + a NEW IP-keyed rate-limit variant (allow_action keys on user.id, can't throttle anon); is_staff gating pattern
**Touches:** apps/places/models.py (PartnerApplication) + services.py + admin.py; apps/donations/models.py (InKindContribution) + services.py; apps/web/views.py + urls.py (partners_apply, transparency in-kind, staff approval) + templates + IP-keyed rate-limit helper; migrations: places + donations

### F26 — Capacity waitlist with auto-promote  `[M/imp2/low/revise]`
*Theme: Filling every seat fairly*

**Pitch.** When an activity is full, let eligible users join a private FIFO waitlist; when a member voluntarily leaves and a seat frees, promote the oldest waitlisted person into the normal join flow — re-gated for cohort/consent/blocking at promote time, with no roster or rank shown to anyone else.

**Why it fits the invariants.** WAITLISTED is excluded from voting_members/participant_count, so it adds no cross-cohort surface; the self-only state shows no count/rank/roster (no social proof, no vanity metric); no new PII or behavioural rollup; the promote-time re-gate re-asserts cohort + can_participate + not-blocked, mirroring _evaluate_vote, so cohort isolation and the no-adult↔minor wall hold.

**Sketch.** Add Membership.State.WAITLISTED (a migration; naturally excluded from voting_members/participant_count since both filter state=MEMBER). request_to_waitlist fires ONLY when can_join fails SOLELY on capacity and all other gates pass — refactor can_join to return a typed reason. On voluntary leave_activity call _promote_from_waitlist, which under select_for_update(skip_locked) pops the oldest WAITLISTED row, RE-GATES, then admits. TWO real decisions: (1) Auto-promote must NOT silently bypass join-by-vote — recommend promote-to-REQUESTED (re-enters vote, preserves join-by-vote) over direct _admit (a vote bypass needing sign-off). (2) uq_membership_activity_user has no state column and the rejoin check is .exclude(REMOVED).exists() — a prior-REMOVED user can't get a second row, so request_to_waitlist must reuse the row, not create one. DROP the no-show claim: there is no member-eviction path today (REMOVE hides content; SUSPEND/BAN deactivate the user but never set state=REMOVED), so only voluntary leave frees a seat.

**Reuses:** can_join refactor to typed reason; leave_activity promote hook + _promote_from_waitlist (select_for_update/skip_locked re-gate); uq constraint/rejoin-row reuse; _evaluate_vote re-gate pattern
**Touches:** apps/social/models.py Membership.State (WAITLISTED + migration); apps/social/services.py (can_join refactor, request_to_waitlist, leave_activity hook, _promote_from_waitlist); apps/social/views.py ActivityViewSet action + apps/web join-button states + templates; apps/social/serializers.py MembershipSerializer self-only waitlist state

### F27 — 'Gauge interest' lightweight pre-activity poll  `[M/imp3/med/revise]`
*Theme: Closing the find→commit loop*

**Pitch.** An ephemeral, auto-expiring "I'd come" gauge for a place+type+coarse-time that converts into a real Activity once a small threshold of same-cohort peers signal — a deliberately throwaway proto-meetup, distinct from the persistent standing Group.

**Why it fits the invariants.** Lowers the commitment to start a meetup, the genuine cold-start problem in a thin launch city. The interest signal lives in a new ActivityInterest m2m that never touches Membership, so connections.shares_activity can't see it — interest co-presence can NEVER enable can_connect. Same-cohort + can_participate + not-blocked scoping; member-only COUNT only; coarse-time + capped lifetime + ops-expiry. No PII, no photo/feed surface, no behavioural rollup. Postgres-only.

**Sketch.** Add social.ActivityInterest (proposer, place FK, activity_type FK, cohort pinned from proposer, coarse window, expires_at) + interested_users m2m. propose_interest gates on can_create_activity; mark_interested gates on same-cohort + can_participate + not-blocked (idempotent get_or_create). convert_to_activity calls create_activity verbatim then fans a JOIN-style invite to interested peers EXCLUDING blocked pairs; reuses the F40 setdefault prefill. A new expire_interest command (in DUE_JOBS beside expire_arrivals) deletes stale rows. Web shows a "gauge interest" variant + a count-only detail. One new mutable Kind INTEREST_CONVERTED. REVISE: position it explicitly as the EPHEMERAL/threshold sibling of the now-shipped persistent Group (not a second standing surface) — short default lifetime, low threshold, silent expiry, clear "temporary until it converts" framing so a failed gauge doesn't read as a dead room.

**Reuses:** create_activity + visible_activities/can_see_activity gates; accounts.can_participate; safety blocked_user_ids; ops run_due_jobs DUE_JOBS (expire_interest); notifications notify() + new mutable Kind + no-op makemigrations; web activity_create F40-style setdefault prefill; connections.can_connect/shares_activity pinned by regression test; social.Group (shipped) — positioned as the ephemeral sibling, not a duplicate
**Touches:** apps/social/models.py (ActivityInterest + interested_users m2m); apps/social/services.py (propose_interest/mark_interested/convert_to_activity); apps/ops command expire_interest + DUE_JOBS entry; apps/web/views.py + templates (gauge variant + count-only detail); apps/notifications/models.py (INTEREST_CONVERTED) + makemigrations; apps/social/tests

### F28 — Guardian "arrived & wrapping up" bookends for child meetups  `[M/imp2/low/keep]`
*Theme: Actually showing up*

**Pitch.** Add the missing second beat to the guardian pickup story: when a CHILD-cohort meetup auto-completes, send each member's active guardian(s) a fixed "the meetup has finished" notice — same relationship-keyed, no-location, no-text path as the existing arrival ping — so a parent doing pickup knows it's over without any tracking.

**Why it fits the invariants.** The notice reaches ONLY the child's own ACTIVE-GuardianRelationship guardians, keyed on the relationship exactly like the audited mark_arrived loop — never a loose flag, never an adult↔minor contact path. Fixed server-composed copy, no free text, no who-stayed roster, no location, no per-user attendance history. A WRAP_UP kind stays mutable. No photo/feed surface. Postgres-only.

**Sketch.** Factor the guardian fan-out inline in mark_arrived into a shared helper (CHILD cohort + ACTIVE GuardianRelationship + dedupe + skip blocked pairs). The completion path is the real work: auto_complete_activities is a bulk .update() and complete_activity is housekeeping-only with no per-row caller, so the bulk update must be reworked to SELECT the just-completed CHILD activities, load each member's active guardians, and fan out a new mutable WRAP_UP with a fixed reason. CRITICAL: notify() never dedupes (always .create()s) and the 12h-grace cron re-runs, so idempotency needs an explicit marker — a wrap_up_notified_at field on Activity (requires a migration). DROP the sketch's "/wards/ gains a finished state": the wards manifest filters OPEN + upcoming, so a completed meetup simply vanishes — ship the notification only.

**Reuses:** shared CHILD/ACTIVE-GuardianRelationship fan-out helper factored from mark_arrived + idempotent via a sent-marker; auto_complete_activities reworked to a per-activity completion loop selecting just-completed CHILD activities; Activity.wrap_up_notified_at field + migration (no spare field exists); notifications mutable WRAP_UP Kind + WHY_REASONS + makemigrations; accounts GuardianRelationship (ACTIVE); safety.blocked_user_ids
**Touches:** apps/social/services.py; apps/social/management/commands/auto_complete_activities.py; apps/social/models.py; apps/notifications/models.py; apps/social/migrations + apps/notifications/migrations

### F29 — Verified-adult supervisor seat for children's activities  `[L/imp3/med/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** Let a CHILD-cohort activity REQUIRE that the owner's own verified, consented guardian be present as a read-only supervisor before any join settles — turning "an adult will be there" into a structural pin, WITHOUT loosening which adults may enter a children's thread.

**Why it fits the invariants.** The only adult who can ever enter stays keyed on an ACTIVE GuardianRelationship and stays read-only (post_to_thread, toggle_reaction, mention_roster, voting_members all already exclude GUARDIAN). Text-first, no ads/metrics, no PII. It refines the existing guardian-accompanied seam. The REVISE keeps it inside the invariant: the candidate's proposed loosening to "guardian of ANY participant" MUST be dropped because it widens cohort isolation.

**Sketch.** Add a SUPERVISED boolean to CHILD Activity (set only at create, NOT in ACTIVITY_EDITABLE_FIELDS which deliberately freezes the cohort-isolation boundary; a post-create toggle is a separate guarded service). Gate _admit (the single settle point for _evaluate_vote and owner_admit): refuse to settle unless a Membership role=GUARDIAN exists whose user is_guardian_of the OWNER — keep add_guardian's existing is_guardian_of(guardian, owner) check; do NOT loosen to "any participant" (that would let child B's guardian observe a thread containing child A, a minor G has no relationship with — a new adult→other-people's-minors read-window, and at bootstrap "any participant" == owner anyway). Re-check on leave/remove for BOTH the guardian leaving AND the supervised participant set, and surface a LIVE chip computed at read time from current memberships — never a chip that lies after the guardian left. Handle the bootstrap (don't deadlock the settle; clear "add your guardian first" message).

**Reuses:** Activity.supervised flag + migration (no-op makemigrations); live-state supervision gate inside _admit keyed on is_guardian_of(guardian, OWNER) only; re-check on leave/remove; set/clear as a guarded service NOT an ACTIVITY_EDITABLE_FIELDS entry; accounts is_guardian_of (existing, ACTIVE — unchanged); web supervised checkbox + LIVE computed chip + wards live reflection; docs/SAFETY.md: document the supervised-seat invariant + that add_guardian is NOT loosened
**Touches:** apps/social/models.py; apps/social/services.py; apps/accounts/services.py; apps/web/views.py; apps/web/templates/web/_activity_card.html + wards.html; docs/SAFETY.md

### F30 — Minor-group two-way "ask the organiser" relief valve  `[M/imp3/low/revise]`
*Theme: Belonging for newcomers*

**Pitch.** Give minor-group members a private, fixed-prompt-only way to flag a question to the group's STAFF organiser — never a thread post, never member-visible — so an announcement-only minor group isn't a mute one-way board.

**Why it fits the invariants.** Every CHILD/TEEN group thread is announcement-only, so a minor group truly is a one-way board. This adds inbound child voice WITHOUT opening any adult↔minor private-contact path: the only recipient is the group's staff curator (minor groups force is_staff_curated + actor.is_staff and there is no ownership-transfer service, so the target is always a vetted adult). Fixed prompts (no free text) remove the grooming/PII vector. Writes NO Post.

**Sketch.** New social.group_ask_organiser(child, group, prompt_choice): assert group is a minor-cohort Group + caller is a current GroupMembership MEMBER + can_participate; validate prompt_choice against a small fixed enum (NO free text); rate-limit via allow_action in a dedicated bucket; record_audit; then _notify(group.owner, Kind.GROUP_QUESTION) routed ONLY to group.owner — never thread_members fan-out, never a Post, never member-visible. Add GROUP_QUESTION to Notification.Kind (mutable, fits max_length=24) + WHY_REASONS + no-op makemigrations. CRITICAL HONESTY: the organiser's only reply channel is a GROUP_ANNOUNCEMENT to the whole group — there is deliberately NO private adult→minor reply, so answers are public-to-the-group; the sketch must own this asymmetry. Before building, weigh whether a tiny fixed-prompt set justifies a new Kind + 4 surfaces vs. a simpler static "how this group works / who runs it" legibility card.

**Reuses:** Public Groups (Group/GroupMembership + announcement-only minor-thread rule) — merged; notifications notify() chokepoint + Kind/WHY_REASONS/NON_MUTABLE pattern (max_length=24); safety.allow_action + record_audit; Minor onboarding enabled (minor_onboarding_enabled gate) for live minor groups; no-op makemigrations notifications
**Touches:** apps/social/services.py (group_ask_organiser); apps/notifications/models.py (GROUP_QUESTION Kind + WHY_REASONS, mutable; + makemigrations); apps/web/views.py + groups-detail template (minor-member-only control, RO/EN prompts); apps/social/views.py (GroupViewSet action); apps/social/tests (no-Post, owner-only target, no member↔member, rate-limit, mutable-kind)

### F31 — Pictographic activity-type chips (+ optional later "simple words" mode)  `[S/imp3/low/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** Give every activity type a fixed server-side glyph so pre-literate children, low-literacy adults, and non-native speakers can recognise and pick activities by picture, not dense text — shipping the glyph chips now and deferring the heavier "simple words" toggle.

**Why it fits the invariants.** On-mission for a children-first product launching in a multilingual EU city. The glyph keys on the ActivityType/category (a thing, never a person), so cohort isolation, the adult↔minor wall, blocking, and PII minimisation are untouched. Structurally the already-reviewed reaction-emoji allowlist model (fixed, non-extensible, server-side Unicode/inline-SVG — no upload), so text-first and no-tracking hold.

**Sketch.** PHASE A (ship now, S): add a FIXED, non-extensible dict in apps/taxonomy keyed on ActivityType.slug with an ActivityCategory.slug fallback (both exist), derived at read time like accessibility_facts — no migration, no field. One helper returns a glyph or a neutral default; an unmapped slug never blocks. Render as a leading glyph on _activity_card.html, interests.html, and the type chips in activity_form.html, reusing the existing .tag pill CSS. PHASE B (defer to a separate item): the "simple words" display preference — a per-user flag mirroring AccessPreference + a /access/-like route, swapping help text for hand-authored plain-language strings through the locale/ro catalog. Split out because its real cost is content authoring/translation (not code) and its strings can drift stale with no test to guard quality — it should not gate the cheap, high-reach glyph win.

**Reuses:** taxonomy: fixed ActivityType.slug→glyph dict + ActivityCategory.slug fallback helper, read-time (precedent: DEFAULT_REACTION_EMOJIS / accessibility_facts); web templates _activity_card/interests/activity_form + existing .tag chip CSS; PHASE B only: per-user display_preference flag + settings route + plain-language strings through locale/ro
**Touches:** apps/taxonomy (fixed slug→glyph map + category fallback helper); apps/web/templates/web/_activity_card.html + interests.html + activity_form.html; static/css/base.css (.tag glyph spacing)

### F32 — Richer accessibility facts + needs-aware honest sort  `[M/imp2/low/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** Widen the read-time accessibility facts beyond 4 OSM keys and let a user's stated AccessPreference STABLE-SORT confirmed-accessible venues to the top of the JS-free places list — never hiding unknown-accessibility places — while dropping the dishonest prefers_quiet promise.

**Why it fits the invariants.** Pure read-time derivation from existing OSM tags (ingest-safe, no writeback), the F15 pattern. AccessPreference is user-STATED, never inferred. The soft-sort-that-never-hides-unknowns rule means nothing is silently excluded — a nudge not a filter. Touches no child-safety surface, no contact path, no photo/feed surface.

**Sketch.** Facts half (the solid core): _tristate is already key-agnostic, so widen accessibility_facts()/_FACT_LABELS to a few more honest top-level OSM keys that map cleanly to yes/no (pick only keys with real values — entrance/door width is NOT clean), add the matching AccessPreference need-booleans (one migration) + matches_access_preference branches + the checkboxes. Sort half (the real cost): there is NO single shared sort path — places_list and NearMeView sort PLACES, while _order_feed_by_location sorts ACTIVITIES. A needs-aware nudge must materialize each queryset and Python-stable-partition confirmed-matches first WITHOUT regressing the existing distance/name/soonest order. Scope v1 to the PLACES surfaces only; leave the activity feed for a follow-up. DROP the prefers_quiet promise: the model comment is explicit that no OSM tag honestly satisfies "quiet/sensory-friendly" — keep it stored-and-dead (or remove it) rather than over-assert.

**Reuses:** accessibility_facts, _FACT_LABELS, matches_access_preference; AccessPreference need-booleans + migration; places_list stable-sort + access_preferences/places_list templates; NearMe ?accessible nudge; prefers_quiet MUST be dropped (no honest OSM source)
**Touches:** apps/places/services.py; apps/places/models.py (AccessPreference need-booleans + migration); apps/web/views.py (places_list stable-sort); apps/web/templates (access_preferences.html + places_list.html); apps/discovery/views.py (NearMe ?accessible nudge); apps/places/tests + apps/web/tests

### F33 — On-device pre-send safety nudge (no server inference)  `[M/imp3/med/revise]`
*Theme: Privacy & data-dignity as product*

**Pitch.** A calm, dismissible client-side "are you sure?" when a thread post looks about to share a phone number, address, or "meet me alone" plan — nudging the author to keep coordination in the meetup, never blocking them and never auto-reporting them.

**Why it fits the invariants.** Surfaces the highest-harm leak (contact details / solo off-platform plan) at authorship time. Deterministic regex (no cloud AI); nothing leaves the device; text-first preserved; the server-side half routes through the EXISTING post_to_thread, so cohort isolation and the no-adult↔minor gate are untouched (the nudge sits strictly downstream). A one-shot dismissible confirm is the opposite of a dark pattern.

**Sketch.** Two halves over the purpose-built MessagePolicy seam. Server: a new MessagePolicy subclass exposing a deterministic phone/email/RO+EN-address/"meet alone" matcher; it MUST stay a soft, non-blocking signal — does NOT set allowed=False (would block the user) and must NOT auto-file a moderation report (auto-reporting "meet at my place" would chill legitimate logistics and flood the child-safety queue). Safest shape: the canonical post_to_thread path stays a pure no-op pass-through; the existing ReasonCode.OFF_PLATFORM report remains the human recourse (no migration, no parallel Post flag). Client: a vanilla-JS matcher (same ruleset, emitted once to avoid drift) that must hook AHEAD of the existing compose-submit handler — which already intercepts submit to send over the WebSocket with file-upload and socket-closed POST fallbacks. The confirm has to gate BOTH the WS-send branch and the plain-POST fallback, or it silently no-ops in the common socket-open case. No-JS users unaffected; confirm is text-only, dismissible, never blocking.

**Reuses:** chat MessagePolicy/ProcessedMessage seam + CHAT_MESSAGE_POLICY (ProcessedMessage.redacted is unused in post_to_thread); post_to_thread single write path (keep it a non-blocking pass-through — NO blocking/auto-report branch); safety ReasonCode.OFF_PLATFORM (reuse, no parallel flag/migration); activity_detail compose submit is ALREADY WebSocket-intercepted — hook ahead on both WS-send and POST-fallback; shared ruleset emitted once (server lib + static JS) to prevent drift
**Touches:** apps/chat/policy.py (new MessagePolicy subclass / shared matcher lib); apps/social/services.py post_to_thread (verify non-blocking pass-through); apps/web/templates/web/activity_detail.html compose form + scripts; static/ JS module emitting the shared ruleset

### F34 — Your activity log (read-only view of your own audit trail)  `[S/imp3/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** A calm, chronological, plain-language list of the safety-relevant actions YOU took — drawn from the tamper-evident audit log you already generate — so transparency isn't one-directional.

**Why it fits the invariants.** Reads ONLY events already recorded for safety/legal reasons — no new tracking, no behavioural rollup, no vanity metric. Read-only, self-scoped, creates no adult-minor contact surface: the fixed {label, when}-only projection never reveals the other party in an interpersonal event, and a CHILD seeing "a guardian began observing your messages" is legitimate F13-style legibility about their own data. No photo/feed surface. Mirrors the F19 self-scoping discipline.

**Sketch.** Add audit_log_for(user, *, limit=N). Filter AuditLog by actor_ref == user.id (the actions the user took) — the primary, safe axis. Project EACH row through a FIXED, hardcoded event→friendly-RO/EN-copy map; an event NOT in the map is DROPPED, never rendered raw. Emit ONLY {event_label, when} — never target_ref, never the data JSON payload (it carries conversation_id, erased_public_id, key_id, sha256, reason — all leaky), never the raw event code. Cap the result set. Then one /my-activity-log/ web view + url + template + a _nav_context line, cloning the F19 safety_record view. De-dupe vs F19 at report.filed. Add a mirror self-scoping test: another user's actions never appear, an unmapped event is dropped (not shown raw), no raw target_ref/data/event-code/other-user-id leaks.

**Reuses:** safety AuditLog (actor_ref:int, target_ref, data:JSONField) + record_audit — existing; safety safety_record_for self-scoping pattern (F19) to mirror; web safety_record view + _nav_context + urls — existing template to clone; i18n RO/EN FIXED event→label copy-map (the load-bearing allowlist); safety test_safety_record.py — existing test to mirror
**Touches:** apps/safety/services.py; apps/safety/tests (new self-scoping test); apps/web/views.py + urls.py; templates/web/; apps/web/views.py:_nav_context

### F35 — Download my data (one-click GDPR portability)  `[S/imp3/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** Surface the already-built GDPR Art.20 export as a plain-language /my-data/ web page with a human-readable summary plus a download, so data dignity is felt rather than buried in a DRF endpoint no UI can reach.

**Why it fits the invariants.** build_user_export is an explicit field allowlist: no DOB, no identity data, no payment-card data, no other-member PII (guardianship/consent links are by public_id only). No new model, no new data, no fan-out, no public/photo surface — fully text-first. The guardian variant reuses is_guardian_of, which keys on an ACTIVE GuardianRelationship, so it creates no adult↔minor contact path.

**Sketch.** Add @login_required web view my_data at /my-data/ mirroring the my_donations/safety_record sibling pattern: call build_user_export(request.user), render a readable on-page summary (profile, age band, cohort, consents, memberships, donations); add the nav link. Offer the same dict as a JsonResponse with Content-Disposition: attachment behind ?download=1. Guardian variant: a "download this ward's data" action on /wards/ that re-checks is_guardian_of exactly as guardian_revoke does (gate on the ward's resolved object, then build_user_export(ward)) — never trust the URL id alone (IDOR). Set login_required, attachment disposition, and application/json correctly. Verified non-duplicate: the API endpoints exist (DRF only); apps/web has NO my-data route.

**Reuses:** accounts.export.build_user_export (reused as-is, side-effect-free allowlist); accounts.is_guardian_of (ACTIVE-link gate, reused for the ward variant); web _nav_context + my_donations/safety_record sibling pattern; web urls route registration
**Touches:** apps/web/views.py; apps/web/urls.py; templates/web/my_data.html; templates/web/ nav include; apps/web/tests (ward IDOR + login_required)

### F36 — What we know about you — a single privacy front-door  `[S/imp3/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** One authenticated, self-only screen that names every category of personal data the platform holds about you and deep-links to the existing control for each, plus honest "we do NOT collect" negative-space statements — turning scattered controls into a felt-privacy front door.

**Why it fits the invariants.** Adds NO new data, NO model, NO behavioural signal — it only re-renders services already strictly self-scoped and field-allowlisted (assurance_provenance band-only; safety_record_for capped/allowlisted; muted-kinds/why-reason from the F31 choke point). It cannot widen exposure beyond what those services already permit, and introduces no public/photo/feed surface and no engagement mechanics.

**Sketch.** Add a single authenticated, self-only /my-privacy/ web view + url + template (no user_id param). It composes already-built reads: assurance_provenance (band/method/provider/dates only), get_muted_kinds + why_reason/WHY_REASONS, a safety_record_for count, the UserInterest list, a donations count, and static factual negative-space copy ("we never store your location", "we store an age band, not your DOB"). Each category deep-links to its existing edit/appeal screen: /access/, /guardianship/ (when has_guardians), /my-safety-record/, /my-donations/, /account/delete/. Reuses the F13/F19/F31 read-only panel pattern + the _nav_context entry. CORRECTION: there is NO GDPR Art.15 export route today (only /account/delete/) — so the page must NOT link a non-existent "export" route; omit it or state "data export is not yet available" (or pair with F35). i18n RO/EN.

**Reuses:** accounts assurance_provenance (band-only); notifications get_muted_kinds + why_reason/WHY_REASONS; safety safety_record_for (self-scoped, allowlisted, capped); Existing self-data routes: /access/, /guardianship/, /my-donations/, /my-safety-record/, /account/delete/; web _nav_context + UserInterest + donations count reads; i18n RO/EN strings
**Touches:** apps/web/views.py (my_privacy view + _nav_context entry); apps/web/urls.py (/my-privacy/); apps/web/templates/web/my_privacy.html; locale RO/EN strings

### F37 — Plain-language Statement-of-Reasons rewriter  `[S/imp3/low/keep]`
*Theme: Privacy & data-dignity as product*

**Pitch.** Replace the hard-coded English DSA Art.16/17 moderation/reporter notices with a deterministic, offline phrasebook keyed on (Action × ReasonCode × cohort × language) so a teen or non-native speaker actually understands what happened, why, and that they can contest it — with the same phrasebook feeding the F19 self-safety-record so the two never drift.

**Why it fits the invariants.** The notice goes only to the affected user about their own account/content (no adult↔minor path), carries enum-label-derived text not free input or PII, is curated/template-only with no ML or per-user cloud spend, and adds no feed/photo/tracking surface. Non-mutability is preserved automatically: MODERATION and SYSTEM are already non-mutable and this change never alters Kind.

**Sketch.** Add a deterministic dict-based phrasebook keyed (Action, ReasonCode, Cohort) → a gettext-marked short sentence, with a graceful fallback to the current generic body for any unfilled cell. CHILD/TEEN cohorts get a gentler, plainer variant; ADULT/UNASSIGNED keep precise wording. Wire it into _notify_statement_of_reasons (reading recipient.cohort) and _notify_reporter. CORRECTION: the moderation body is currently a hard-coded English f-string with NO gettext wrapping and NO existing RO catalog entries — so this introduces _() marking and new locale/ro msgids (not merely "reuse the catalog"), and depends on Phase-2 i18n for real RO coverage. Refactor the shared label-rendering so safety_record_for (F19) consumes the SAME phrasebook, otherwise the self-record will still show jargon labels while notices read plainly. No model/migration; keep MODERATION/SYSTEM untouched.

**Reuses:** Legal/DSA sign-off (Phase 2) to confirm rewritten phrasings stay Art.16/17-compliant — the dominant cost; Deeper i18n (Phase 2): the notice path is NOT gettext-wrapped and has no RO msgids today
**Touches:** apps/safety/services.py _notify_statement_of_reasons; apps/safety/services.py _notify_reporter; apps/safety/services.py safety_record_for (F19 — share the phrasebook); locale/ro (new gettext msgids); apps/accounts User.cohort (read-only)

### F38 — Offline-resilient 'my next meetups' card  `[M/imp3/med/revise]`
*Theme: Accessibility & inclusion*

**Pitch.** A lean, network-first /my-meetups/ page (time, place, meeting point, safe-exit guardians) with a freshness-stamped offline fallback, so a member on a cheap Android with patchy data can still read the essentials en route — without ever being silently shown a stale or cancelled meetup.

**Why it fits the invariants.** Serves the find-people-and-go promise for the realistic launch demographic and is a child-safety positive (meeting point + named guardians readable offline). Text-first, no-JS-baseline, no push/background-sync/tracking. The risk is caching authenticated cohort-scoped safety data on-device, which the reshaped design neutralises (per-user hard cache key, logout purge, freshness stamp, no silent stale safety claims).

**Sketch.** Add a tiny @login_required /my-meetups/ view reusing the F6 wards query shape but self-scoped to the viewer, plus the F5 my_guardians + meeting_point context, under a strict size budget; works fully without JS. Register a minimal root-scoped service worker (no SW today; STATIC_URL scopes to /static/, so it needs a dedicated root-served url/view or a Service-Worker-Allowed header). Make the SW NETWORK-FIRST for this page with a clearly-stamped offline fallback ("saved at HH:MM — may be out of date; cancellations may not show"), NOT generic stale-while-revalidate, because cancel/edit re-notify changes the SW never saw must not be presented as live truth. Hard per-user cache key + purge on logout (stock LogoutView gives no hook — add a thin logout step or SW message). Cache base.css alongside. Tests for offline, no-JS, and cross-user (shared-phone) paths.

**Reuses:** wards upcoming-meetups query reused/self-scoped; safe-exit my_guardians + meeting_point context; visible_activities / Membership MEMBER filter for cohort-safe self-scoping; WhiteNoise static serving + a root-served SW url or Service-Worker-Allowed header; Custom logout cache-bust (stock LogoutView has no hook); update_activity re-notify + cancel_activity (informs network-first + freshness-stamp, not SWR)
**Touches:** apps/web/views.py (lean self-scoped view + thin logout cache-bust); apps/web/urls.py (/my-meetups/ + root-scoped SW route); apps/web/templates/web/ (minimal template + service-worker file); static/css/base.css

### F39 — Greeter role — a named member who looks out for first-timers  `[S/imp2/med/revise]`
*Theme: Belonging for newcomers*

**Pitch.** When a newcomer is admitted, name an existing member as their point of contact in the welcome itself — so the shipped F39 "say hello" nudge has a real, friendly destination instead of speaking into a void.

**Why it fits the invariants.** Gives a shy first-timer a named, same-cohort peer. Text-first, behind the membership wall, no PII beyond a display name already visible to members. The hard guard — greeter must be a current MEMBER with role != GUARDIAN — has an exact precedent in voting_members, so a supervisory guardian is never surfaced to a CHILD newcomer; cohort isolation pins the activity, so a greeter is always a same-cohort peer.

**Sketch.** Reshape from a persistent member-list badge into a transient per-activity helper signal whose primary payoff is the welcome line, not a name-tag. Add Membership.is_greeter (BooleanField default False), reset on leave by appending it to the leave_activity update_fields. New atomic set_greeter(owner, activity, member): owner-only, target must be a current MEMBER, role != GUARDIAN, same activity, audited. Extend the _admit body to append "Greeters here are X — happy to help" when greeters exist. KEY RESHAPE to stay clear of invariant 2: keep the activity_detail display deliberately minimal and unranked (a plain member-only "Ask a greeter: X" line, never a count, never a sortable badge), and do NOT expose a per-user greeter tally or "greeter on N activities" anywhere — the flag stays strictly per-activity and is never read cross-activity.

**Reuses:** F39 first-timer welcome mat (_admit body + welcomed_at TTL banner); Membership transient-flag + reset-on-leave pattern (leave_activity update_fields); owner-only service guard pattern + safety.record_audit; Role.GUARDIAN exclusion precedent (voting_members) + cohort isolation
**Touches:** apps/social/models.py (Membership.is_greeter); apps/social/services.py (set_greeter, _admit body, leave_activity reset); apps/web/views.py activity_detail() + template; apps/social/views.py MembershipViewSet

### F40 — Newcomer-friendly badge + honest 'new faces welcome' reason  `[S/imp2/low/revise]`
*Theme: Belonging for newcomers*

**Pitch.** Let an organiser mark a meetup as actively welcoming first-timers, surfaced as a calm, honest discovery chip and optional filter — so a shy newcomer finds groups that explicitly want strangers instead of guessing which are tight-knit regulars.

**Why it fits the invariants.** A purely owner-stated intent — never inferred, never counted, no per-user history or vanity metric. Rides the existing cohort-isolation/visibility gates, opens no adult↔minor private-contact path, touches no PII/consent/photo surface, adds no engagement loop. Identical safety posture to the already-merged beginners_welcome (F17).

**Sketch.** DECISION TO RESOLVE FIRST: whether this is a distinct flag or a relabel of beginners_welcome. The honest, higher-leverage version is NOT a new boolean — an organiser who ticks beginners_welcome will reflexively tick this too, producing welcome-washing and card clutter, and the two filters fragment the same intent. Recommended: (1) sharpen the EXISTING beginners_welcome chip to read as a belonging signal and ensure it can drive an honest rec_reason (set a.rec_reason to "new faces welcome" only when the flag is set AND no stronger interest/proximity reason already won), OR (2) if a genuinely distinct axis is wanted, a 3-state organiser choice on ONE field (skill-level vs. social-warmth) rather than a parallel bool. Add the ?welcomes_newcomers filter branch only where it composes with the existing F17 filter. Keep the cheap, safe, honest-reason mechanism; fold it into beginners_welcome instead of minting a redundant flag.

**Reuses:** beginners_welcome (F17) — default-False bool in ACTIVITY_EDITABLE_FIELDS with web filter branches + template chips; home() rec_reason loop (honest-reason hook); activity_list() + discovery ActivitiesFeedView (the latter lacks the beginners filter today — small net-new for parity)
**Touches:** apps/social/models.py (only if a new field/3-state is chosen); apps/social/services.py (create_activity kwarg + ACTIVITY_EDITABLE_FIELDS, or relabel); apps/web/views.py home()/activity_list(); apps/web/templates/web/_activity_card.html + activity_detail.html; apps/discovery/views.py ActivitiesFeedView

### F41 — "What to expect when you arrive" structured first-time card  `[S/imp2/low/revise]`
*Theme: Belonging for newcomers*

**Pitch.** A member-only "First time here?" card that answers the unspoken social newcomer questions (how to recognise the group, what happens first), emphasised during the existing first-timer welcome window — built as ONE new owner-curated field, not two, to avoid duplicating F9 logistics.

**Why it fits the invariants.** Owner-curated free text rendered ONLY behind the same is_member wall as the existing F9 logistics card, so it adds no visibility surface and no adult→minor discovery/contact path. No new PII, no location, no tracking, no notifications, no public feed, no photo surface. Lowers the social drop-at-the-door barrier for a nervous newcomer (especially a child/teen turning up alone). Reuses the F9 gated-card render + F39 welcome-window pattern.

**Sketch.** Add ONE owner-curated TextField first_time_note to Activity (+ migration), routed through the F2 edit path (ACTIVITY_EDITABLE_FIELDS + serializers, capped by LOGISTICS_FIELD_MAX_LENGTH like accessibility_notes). Do NOT add how_to_find_us: "how to recognise the group / where exactly to meet" is already F9 meeting_point, and a generic note is already organizer_note — adding it would duplicate them. Render first_time_note in a new member-gated "First time here?" card reusing the logistics-card pattern (linebreaksbr on autoescaped text); the card is visible to ALL members (so the field is not write-only after 7 days) and merely gets a calm visual emphasis when show_welcome is true — NO badge, streak, countdown, or vanity nudge. draft_activity_text may seed a gentle template default. Even leaner alternative worth considering: add NO field and simply re-present the existing meeting_point/organizer_note under a newcomer-framed heading during show_welcome.

**Reuses:** Activity: ONE first_time_note TextField (+ migration); drop how_to_find_us (duplicates meeting_point); ACTIVITY_EDITABLE_FIELDS + optional gentle default in draft_activity_text; serializers capped by LOGISTICS_FIELD_MAX_LENGTH; web is_member + show_welcome (already computed) — just pass through; activity_detail.html new member-gated card reusing the logistics pattern, calm emphasis under show_welcome, visible to all members; F9 logistics card + F36 draft_activity_text + F39 welcome window
**Touches:** apps/social/models.py Activity + ACTIVITY_EDITABLE_FIELDS; apps/social/services.py; apps/social/serializers.py; apps/web/views.py activity_detail(); apps/web/templates/web/activity_detail.html + activity_form.html + activity_edit.html

### F42 — Partner-credited earmarked campaign  `[S/imp2/low/revise]`
*Theme: Nonprofit sustainability & civic impact*

**Pitch.** Let an earmarked Campaign name a verified civic Partner and a concrete outcome ("Cluj City Library — fund the Saturday reading hour"), surfacing a one-line text credit beside the existing calm static progress bar — making a donation ask tangible and partner-anchored without any new payment, write-class, or owner fiction.

**Why it fits the invariants.** Reuses F34's two-section /transparency pattern (raised total beside staff-entered SpendEntry rows, never an "X of Y goal" bar) and F37's Partner.objects.public() chokepoint (verified+active only, text-only, 280-char blurb, no logo/rank field). No new contact path: a Partner has no user. No new behavioural write. Cohort isolation, child-safety, and the private-contact wall are untouched.

**Sketch.** Add a nullable Campaign.partner FK (on_delete=SET_NULL, null/blank). Gate the choice to Partner.objects.public() at all three layers F34 already hardens (form/serializer/start_donation) so an unverified/inactive partner can never be named. On /campaigns/ and /transparency/, render Campaign.partner.name + the 280-char blurb as a single static credit line beside the existing calm integer-percent progress bar (reuse |cents + the two-section layout). Optionally extend the place_detail Partner credit. One small migration; no rrule helper, no ops job, no ActivitySeries, no owner/create_activity changes, no auto-materialised activities. (The original recurring-series materialiser half is dropped: social.Group already IS the durable standing home, and create_activity hard-requires a real owner User a Partner doesn't have.) Tests: SET_NULL on partner delete leaves the campaign general-fund-safe; inactive/unverified partner rejected at all three layers; no donor PII; credit absent when partner is NULL.

**Reuses:** F34 donations.Campaign + Donation.campaign SET_NULL + start_donation earmark + calm static progress bar + two-section transparency + |cents; F37 places.Partner + Partner.objects.public() chokepoint + safe_external_url + place_detail credit; social.Group (merged) — subsumes the dropped recurring-series half
**Touches:** apps/donations/models.py (Campaign.partner FK) + migration; apps/donations/services.py start_donation + serializer + web form (3-layer public() gate); apps/web campaigns/transparency templates + optional place_detail credit

### F43 — Auto-expand the E2EE safety-number panel when peers are unverified  `[S/imp2/low/revise]`
*Theme: Privacy & data-dignity as product*

**Pitch.** The full key-verification UI already ships and runs on every conversation open; the only honest delta is to auto-expand the existing collapsed safety-number panel when a peer is unverified, so the fingerprint comparison is zero-click instead of one-click.

**Why it fits the invariants.** Strengthens E2EE trust with no new data, no new contact path, and no surface that could bridge adult to minor — record_key_verification already routes through assert_can_message (same-cohort). Stays text-first and ad/tracking-free. The reshape deliberately keeps it quiet (expand only when unverified, stay collapsed once verified) to honor the no-dark-patterns / no-nag invariant.

**Sketch.** The pitch's premise that the UI is buried is FALSE: a complete key-verification UI already ships and renderVerification() is called unconditionally on every conversation open (per-peer safety number, verified/unverified pill, Mark-verified/Re-verify POST, key-change warning, count summary). The ENTIRE genuine change: in renderVerification() the panel is built as a <details>; set panel.open = unverified > 0 after the loop computes unverified, so a conversation with an unverified peer shows the safety number(s) immediately and a fully-verified one stays collapsed (so it never becomes an always-on nag). Optionally mirror the count into a small inline header chip. No backend, model, migration, or crypto change. Do NOT re-implement the panel — edit the one that ships, or it regresses the working key-change warning and rotation auto-flip.

**Reuses:** messaging verification_status / record_key_verification / key_fingerprint (already built, unchanged); messaging /api/messaging/verify/ + user-key endpoint (already built); static/js/e2ee-messaging.js existing renderVerification() panel (the ONLY file to edit); messages.html #mz-verify mount point + styles; messaging KeyVerification model + migration + test_verification.py (already built)
**Touches:** static/js/e2ee-messaging.js (renderVerification: set panel.open when unverified>0); apps/web/templates/web/messages.html (optional inline unverified chip style)


## Stats

81 raw ideas → 44 after invariant filter (11 rejected) → 43 final across 9 themes.
