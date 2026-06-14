# Feature catalog — 2026-06 ideation, WAVE 2

> Produced by the feature-ideation-catalog workflow (111 agents): map → ideate (11 lenses)
> → cluster/reject invariant-violators → adversarial evaluate → synthesize. Built AFTER the
> original 2026-06 catalog was essentially fully shipped (F1–F9, F11–F22, F25–F43 merged).
> These are NEW candidates that do not duplicate shipped behaviour. Verdicts: keep / revise
> (revise = ships only with the load-bearing fix in its sketch). Effort S/M/L; impact 1-5;
> risk low/med/high. NOTE: WAVE-2 ids (F1..F43) are a fresh namespace — unrelated to the
> original catalog's F-numbers.

## Recommended starter set: F1, F8, F5, F6, F32

A coherent, low-risk first batch that touches all the strongest themes without taking on a high-risk or legally-gated bet. F1 (taxonomy/alias search) is the highest-leverage fix to the core find-people loop — it un-deadens already-seeded RO/EN vocabulary that returns nothing today, and it is the natural substrate other discovery work (F3, F37) builds on. F8 (logistics in the reminder) is a true quick win: S effort, no migration, no new safety surface, and it directly improves show-up for first-timers and CHILD arrivals. F5 (organizer console) is the supply-side keystone — read-only, self-scoped, cohort-safe by construction, and it makes every other organizer feature (F12/F14/F15) more discoverable. F6 (RRULE expansion) is the highest-impact data-quality fix, making recurring community meetups actually appear in Happening. F32 (Art.20 thread-content export) is an S-effort, zero-risk compliance backstop that strengthens the privacy story and pairs naturally with the planned DPIA work. All five are impact>=3, none depend on the unbuilt Phase-2 items, and together they advance discovery, show-up, supply, data, and privacy in one wave.

**Quick wins:** F8, F9, F10, F11, F14, F21, F22, F23, F25, F26, F27, F32, F33, F34, F35, F36  ·  **Big bets:** F4, F17, F20

## Sequencing notes

Sequencing and dependency advice, grounded in the codebase:\n\n1. SEARCH/DISCOVERY chain: Ship F1 (alias/slug search) first — it is the substrate. F3 (event interest gauge), F37 (taxonomy bridge fill) and F19 (watch an activity) all sit on the find-people loop; F37 in particular is a thin sparse-tail polish, so do it after F1 lands and feeds fill. F3 and F19 both reuse the F27 gauge / saved-search opt-in patterns and the at-most-once ledger — build whichever is prioritized, they don't block each other.\n\n2. COMPLETION-PATH gotcha (affects F11, F15, F17): the DOMINANT completion path is auto_complete_activities, a bulk .update() that BYPASSES complete_activity. Any feature that wants to fire on completion (F15 re-run nudge, F17 post-meetup check-in) must either refactor that command to a shared per-activity helper or use a lazy on-first-view flag (the F39 welcomed_at idiom). Decide this once and reuse it across F15 and F17.\n\n3. min_to_go is NULLABLE with no default (verified). This breaks the naive trigger in F19 (watch) — the quorum latch never fires for the common no-quorum activity, so F19 MUST add the all-activities trigger (approaching-start reminder / member-count step) or watchers wait forever. The same nullable fact makes F10 (fallback time) and F11 (RSVP nudge) honest only when an organiser set a quorum, which is fine since both degrade gracefully.\n\n4. CHILD-SAFETY cluster (F2, F16, F18): all three extend the F7 GuardianGuardrail + ACTIVE-GuardianRelationship pattern. F2 must use a nullable timestamp + predicate in _admit (NOT a new Membership.State — F29 proves the lighter pattern; a new state touches ~51 read sites). F18 must route the venue floor through a SINGLE shared helper across ALL FOUR child-venue chokepoints (can_join, create_activity, create_series, F27 gauge) — wiring a subset re-creates a documented leak. F16 must use a NEW MUTABLE kind, never the DSA-reserved SYSTEM channel.\n\n5. F4 (roster vouch) is a BIG BET but legally gated: ship ONLY the verification_source enum + honest labelling now (low-risk, immediately useful); the partner-operator attestation channel is net-new and must wait on the Phase-2 DPIA/RO-counsel sign-off and must NOT auto-unlock minor onboarding. Treat it as two tickets.\n\n6. F20 is the one performance landmine: accessibility_facts() is a free dict-read on four hot list surfaces; making it query-backed without F28's prefetched-tally pattern is a hundreds-of-queries N+1. Do not ship F20 until the prefetched-tally path is committed. F22 (getting-there facts) is the clean, cheap sibling — ship it first as the easy F19-overlay win and explicitly DEFER the prefers_quiet sort-wiring.\n\n7. PRIVACY cluster (F32-F35, F42) composes well and is mostly S effort; F33 also fixes a currently-broken my_privacy.html link. F42 must fold into the existing /my-privacy/ page (no third ledger). F35 and F41 both carry an honesty-drift hazard — gate their copy on legal/DPIA review and add the user_vector input-source guard test for F41 so the recommender claim can't silently go false.\n\n8. DEFERRALS pinned to Phase-2: F24's dedup half (needs a second event source), F36's recurring-pledge half (needs Stripe subscriptions), F40's quiet-hours batcher (needs web-push — notifications are in-app only today). Ship the immediately-valuable half of each and explicitly defer the rest rather than building dead code.\n\n9. F43 (familiar faces) has a verified latent safety inversion in its pitch — the claimed cohort gate DEFAULTS to all cohorts and would surface to minors. If built, it MUST add an explicit Cohort.ADULT guard and DROP the discovery-card count entirely (a raw cumulative count on a discovery surface is the exact inv.2 anti-pattern the F27 remediation already flagged)."}

## Themes

- **Closing the find-people-and-go loop** (F1, F3, F19, F37) — Make the core discovery-to-attendance path actually convert: forgiving search, honest feed fill, and turning latent interest in events into real meetups.
- **Deepening the child-safety promise** (F2, F4, F16, F17, F18, F38) — Per-meetup guardian control, stronger trust anchors, and post-meetup/arrival backstops — all narrowing child access only, never opening an adult-minor path.
- **Reliability and showing up (calm, no shaming)** (F8, F9, F10, F11, F29, F30) — Help groups actually convene — logistics in reminders, transient transit/RSVP cues, a pre-declared fallback, and pro-safety call-offs — with zero per-user reliability rollups.
- **Organizer and facilitator tooling** (F5, F12, F13, F14, F15) — Reduce friction for the volunteer coaches and librarians who seed safe-venue supply: a self-scoped console, reusable formats, per-instance notes, claimable jobs, and a re-run nudge.
- **Place and event data quality** (F6, F20, F21, F22, F23, F24) — Make the 'we already know the places and what's happening' promise true: recurring events, crowd-filled accessibility and getting-there facts, hours corrections, seasonal hours, and cancellation signals.
- **Accessibility and inclusion** (F27, F28, F31) — Serve the lowest-bandwidth, lowest-literacy, and youngest verified members: a data-saver mode, a read-aloud meetup brief, and guardian-assisted setup.
- **Privacy and data-dignity as product** (F32, F33, F34, F35, F40, F41, F42) — Turn the app's strongest differentiator into felt, legible product surfaces: full Art.20 export, an erasure preview, audience legibility, a processors panel, and honest feed/consent transparency.
- **Real-world and civic impact + sustainability** (F7, F25, F26, F36, F39, F43) — Prove and fund the mission: a civic volunteering branch, aggregate impact figures, honest campaign close-outs, donor receipts, and read-time partner credit.

## Candidates

### F1 — Taxonomy-aware, typo-tolerant search  `[M/imp4/low/revise]`
*Theme: Closing the find-people-and-go loop*

**Pitch.** Make activity search forgiving by matching the activity-type slug + the RO/EN aliases the app already seeds (jogging, streetball, volei, alergare, inot...), then layering a bounded depth-1 synonym walk and a trigram typo fallback — with an honest 'also showing / did you mean' line, ordering still soonest-first.

**Why it fits the invariants.** Lives entirely inside visible_activities(viewer), so cohort isolation and block exclusion are untouched — a TEEN searching 'jogging' still sees only their own cohort. Read-only, no model writes, no PII, no audit side-effect. The honest disclosure follows the F17 no-dark-patterns discipline; ordering stays soonest-first (no popularity ranking).

**Sketch.** Extend activity_search_filter / search_activities (apps/social/services.py) so the query resolves against ActivityType.slug AND the aliases JSON, not just activity_type__name — already-seeded RO/EN vocabulary returns nothing today purely because icontains never reads aliases. Then OR in a depth-1 ActivityRelation SYNONYM/VARIANT walk and a TrigramSimilarity zero-result fallback (add a small GIN trigram index on ActivityType.name). Show 'also showing X / did you mean Y' in the two callers. FIX: add the missing RO term 'fotbal' to the football aliases in a seed migration — it is not seeded today.

**Touches:** apps/social/services.py; apps/taxonomy/models.py (+ seed migration adding missing RO aliases incl. 'fotbal'); apps/social/views.py; apps/web/views.py; apps/social/tests

### F2 — Guardian per-meetup join approval  `[M/imp4/med/keep]`
*Theme: Deepening the child-safety promise*

**Pitch.** Let a guardian require that a CHILD ward's join can't settle until the guardian approves the specific place/time/type — turning all-or-nothing consent into informed per-meetup peace of mind, without the guardian ever entering the activity.

**Why it fits the invariants.** Strengthens child safety: it ONLY narrows child access (a hard NARROW like supervised_only). The guardian decides out-of-band via a decide_ward_join action keyed on is_guardian_of and never joins the thread, so no adult-minor contact path is created. The SYSTEM notice carries only the existing wards() allowlist (place/time/type) — no peers, no PII. One nullable timestamp, cleared on leave, never aggregated.

**Sketch.** Add require_join_approval to GuardianGuardrail + one combine line in effective_guardrail. Do NOT add a new Membership.State — mirror F29's fail-closed pattern: a nullable Membership.guardian_join_approved_at + a guardian_join_satisfied predicate in _admit's gate. CRITICAL fail-closed: when ANY active guardian requires approval, ALL must approve (strictest-across-all). On first blocked admit, fan a non-mutable SYSTEM notice to each requiring guardian. decide_ward_join (audited, keyed on is_guardian_of) records approval; the last approval triggers _settle_pending_joins; decline removes the membership (no silent strand). Surface a clear 'waiting on a guardian' pending state to BOTH requester and owner.

**Depends on:** F7 GuardianGuardrail + effective_guardrail; F29 _admit fail-closed + _settle_pending_joins pattern; F6/F18 wards() manifest allowlist
**Touches:** apps/accounts/models.py; apps/accounts/services.py; apps/social/models.py (nullable timestamp, NOT a new State); apps/social/services.py; apps/web/views.py; apps/notifications (reuse non-mutable SYSTEM)

### F3 — Event interest gauge that converts to a real meetup  `[M/imp4/med/keep]`
*Theme: Closing the find-people-and-go loop*

**Pitch.** Mark 'I'd go to this' on a public event; once a coarse same-cohort quorum is reached, the proposer unlocks the one-tap 'organise the meetup for this' prefilled at that event's place/type/time — turning read-only city happenings into people actually convening.

**Why it fits the invariants.** Closes the one still-open loop on the events surface. Rides F27 (the most engagement-neutral primitive): counts-only display, decay/expire, no PII, no roster. Child safety holds structurally — an EventInterest is a plain M2M that never touches Membership, and connections.shares_activity queries ONLY Membership, so a gauge can never feed can_connect.

**Sketch.** Add a sibling EventInterest model (FK to Event, M2M interested_users, cohort, expires_at, converted_activity) — NOT a field on Event (re-ingest would clobber). Cohort is load-bearing: events are cohort-blind, so pin cohort from the AUTHENTICATED USER and read through a new cohort-walled visible_event_gauges primitive (modelled on visible_gauges) so a child never sees an adult-slice gauge. THREE non-negotiables: (1) cohort pinned from the user + a cohort-walled read primitive; (2) F9 is_child_safe_venue precondition for CHILD gauges carried over verbatim; (3) reject placeless/inactive-type events at propose AND convert. Convert reuses convert_to_activity -> create_activity (re-runs every gate). Replicate F27's no-connect regression test.

**Depends on:** F27 gauge machinery; F40 GET-validated activity_create prefill; events.upcoming_events / public-place gate; social.create_activity re-gating
**Touches:** apps/events/models.py (+ migration); apps/events/services.py; apps/social/services.py; apps/discovery/views.py; apps/web/views.py; apps/ops (DUE_JOBS expire_event_interest); apps/social/tests + apps/events/tests

### F4 — School/club roster vouch as a guardian-link trust anchor  `[L/imp4/high/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** A vetted partner organisation (school/club) attests a specific guardian-ward pairing out-of-band, recorded as a higher-trust verification_source on the GuardianRelationship and shown honestly in the /guardianship/ + /wards/ panels — strengthening the mutual-click link until EUDI parental credentials ship.

**Why it fits the invariants.** Targets the single biggest acknowledged child-safety gap: the code itself flags the mutual-click link as 'NOT verifiable proof' and the reason minors stay blocked from prod onboarding. This is that anchor. It only records HOW confidently a link was established, never relaxes cohort isolation or the private-contact wall; stores only a pairing assertion + source enum, no PII (mirrors EUDI's reject-all-PII posture).

**Sketch.** Net-new — do NOT bolt onto the IdentityProvider ABC (verify() returns a per-user age band; a vouch is a TWO-party relationship attestation, a different shape). SHIP NOW: add GuardianRelationship.verification_source enum (mutual_click | roster_vouch | eudi) + migration, thread through link/invite/accept + guardianship_capabilities + both panels as an HONEST TRUST LABEL ONLY (never auto-unlocks a capability). DEFER behind legal: the partner-operator attestation CHANNEL is the bulk of the work and does not exist — places.Partner is text-only with no operator account/auth/vouching workflow. Design that channel as a separately-reviewed follow-up; it must NOT auto-unlock onboarding and must wait on the Phase-2 DPIA/RO-counsel sign-off.

**Depends on:** Phase-2 legal/DPIA/ToS/DSA + RO-counsel sign-off; A vetted partner-organisation registry with AUTHENTICATED OPERATOR ACCOUNTS (net-new; Partner alone is insufficient); record_audit
**Touches:** apps/accounts/services.py; apps/accounts/models.py (verification_source enum + migration); apps/places (net-new authenticated partner-operator surface; deferred); apps/web (/guardianship/ + /wards/ honest trust labels); apps/safety/services.py

### F5 — Organizer console ('Run my meetups')  `[M/imp4/low/keep]`
*Theme: Organizer and facilitator tooling*

**Pitch.** One self-scoped /organize/ dashboard where an organizer sees every activity, series and group they run, each tagged with the concrete action it needs now — pending join requests, supervisor not yet seated, an empty meeting point near start.

**Why it fits the invariants.** Read-only and self-scoped to content the user OWNS or co-organizes. Since create_activity/create_series/Group all pin cohort=owner.cohort, an organizer's own meetups are by definition all in their cohort — no adult-minor or cross-cohort surface can open. Shows COUNTS only (mirroring owner_admit), never requester identities, attendance history, or per-organizer vanity counters. No new write path, model, or notification kind.

**Sketch.** New read-only social.organizer_console(user) composing three existing chokepoints — visible_series(user).filter(owner=user), Activity rows where is_organizer holds (owner OR F22 co-organizer), and visible_groups(user).filter(owner=user) — with per-activity action flags computed live (count of state=REQUESTED memberships, supervision_satisfied, starts_at-near-with-empty-meeting_point). Use a with_counts-style annotation to avoid N+1. Render /organize/ off the you_hub seam + a parity DRF read endpoint; deterministic soonest-first, bounded. Every row LINKS into the existing edit/admit/announce screens — surfaces work, never performs it. REUSE (link, do not re-list) the F38 my_meetups participant list. Ban any per-organizer vanity counter.

**Depends on:** is_organizer / visible_series / visible_groups / supervision_satisfied / with_counts; F22 co-organizer + F29 supervision (source of truth)
**Touches:** apps/social/services.py; apps/web/views.py + urls.py (/organize/); apps/social/views.py (DRF read endpoint); templates/web/organize.html

### F6 — Recurring-event (RRULE) expansion in the iCal parser  `[M/imp4/low/keep]`
*Theme: Place and event data quality*

**Pitch.** Expand a bounded RRULE subset in the dependency-free iCal parser so a weekly chess club or Sunday parkrun surfaces every upcoming occurrence in Happening, not one stale stub.

**Why it fits the invariants.** Serves the 'we already know what's happening' core promise for exactly the recurring community activities the app is built around. Touches only AllowAny public event data — no cohort, PII, photo, or adult-minor surface. Stays dependency-free and Postgres-primary; a fixed forward horizon + occurrence cap, never infinite.

**Sketch.** Add a pure-Python RRULE expander to apps/events/sources.py handling FREQ/INTERVAL/BYDAY/UNTIL/COUNT, with a ~90d horizon and a hard occurrence cap so malformed/unbounded rules are safe. parse_ics emits one RawEvent per occurrence; upcoming_events already filters starts_at>=now so expired occurrences fall off for free. REQUIRED GUARDRAIL: Event.external_id is max_length=200 and UID is already capped at 200 — prepending the feed namespace AND appending the date overflows the column -> silent truncation -> collision against the unique constraint. Derive the per-occurrence id from a length-bounded base so feed<pk>:<base>:<YYYYMMDD> always fits 200. Expand against DTSTART's tz; treat UNTIL per RFC5545. F21 EventReport CASCADE + classify apply per-occurrence for free.

**Touches:** apps/events/sources.py; apps/events/services.py; apps/events/management/commands/sync_event_feeds.py; apps/events/tests/test_events.py

### F7 — Aggregate civic-impact figures (public + staff, never per-user)  `[M/imp4/med/revise]`
*Theme: Real-world and civic impact + sustainability*

**Pitch.** Aggregate-only impact and coverage figures — meetups held by type/area, venue coverage and data freshness, donations-and-spend totals — so the nonprofit can prove real-world good to funders and aim its next ingest run, with a k-anonymity suppression floor so thin launch-city cells can never re-identify anyone.

**Why it fits the invariants.** Serves the nonprofit's survival (defensible grant evidence) and data quality (surfacing the never-shown Place.last_seen_at) using only Count/Sum aggregates over existing models. Org-level reach, not per-user analytics; no new PII; plain Postgres. The donation-transparency restraint (two static numbers, no goal bar) is the design template.

**Sketch.** Extend apps/ops StatsView (IsAdminUser) + add apps/ops/services.py with impact_snapshot()/coverage(): completed meetups by category and by Area, distinct active venues, venues with stale last_seen_at, events/week, disputed-edge counts, donation + spend totals (reuse existing services). Render a calm public /impact/ and staff /coverage/ reusing the no-goal-bar pattern. TWO NON-NEGOTIABLE service-enforced guardrails: (1) a k-anonymity small-cell suppression floor reusing COMMUNITY_K_ANON_FLOOR=5 — any thin by-Area/by-Category cell is collapsed; (2) NEVER cross-tab by cohort and never expose any CHILD slice publicly (a public 'kids' chess in Area X = 1 meetup' figure is a child-locatability leak). Update the exact-keyset stats test + add suppression/no-cohort-leak tests.

**Depends on:** apps/ops StatsView; COMMUNITY_K_ANON_FLOOR precedent; Activity.Status.COMPLETED + cohort (EXCLUDED, not cross-tabbed); donations totals + spend_by_category (reuse)
**Touches:** apps/ops/views.py; apps/ops/services.py (new); apps/ops/tests/test_ops.py; apps/web/views.py + urls.py; apps/web/templates/web/impact.html + coverage.html (new)

### F8 — Logistics baked into the meetup reminder  `[S/imp3/low/keep]`
*Theme: Reliability and showing up (calm, no shaming)*

**Pitch.** The pre-meetup reminder stops being a bare 'starts soon' line and carries the activity's already-curated member-only logistics (meeting point, what to bring, getting-home and first-timer notes), truncated, so members arrive prepared without digging through the thread.

**Why it fits the invariants.** The reminder loop already fans out ONLY to Membership.State.MEMBER rows — the same audience cleared to see these member-only fields — so the visibility wall is unchanged. Text-first, no media, no PII, no location stored, one-shot calm reminder honouring the F31 mute. getting_home_note/first_time_note stay off the cohort-visible serializer; they only land in a notice to confirmed members.

**Sketch.** In send_activity_reminders.py, compose the notify() body from the present logistics fields with labelled lines, each truncated and the whole body capped by a module constant (so a long note can't bloat the in-app or future web-push payload); fall back to today's bare 'Starts {time}.' when all fields are blank. No new model, Kind, serializer, or permission change. The (recipient, EVENT_REMINDER, url) dedup and the _supersede_reminders re-fire-on-time-change path are untouched. Tests: logistics-present body, all-blank degradation, truncation cap, non-members never receive it.

**Touches:** apps/notifications/management/commands/send_activity_reminders.py; apps/notifications/tests/test_reminders.py

### F9 — Ephemeral 'on my way / running late' status  `[S/imp3/low/keep]`
*Theme: Reliability and showing up (calm, no shaming)*

**Pitch.** Alongside the existing 'I've arrived' ping, a member can tap a fixed 'on my way' or 'running ~10 min late' status so the group holds the start a moment — zero free text, zero location, and it self-erases so it never becomes a punctuality history.

**Why it fits the invariants.** Inherits every safety property of mark_arrived by construction: fixed enum + server-constant late bucket (no child-authored string reaches an adult), no location, CHILD-only guardian fan-out keyed on an ACTIVE GuardianRelationship, blocked pairs excluded, audited. Crucially CLEARED by expire_arrivals + reset on leave, so it is an ephemeral cue, never a per-user reliability rollup.

**Sketch.** Add Membership.transit_status (NONE/ON_MY_WAY/RUNNING_LATE) + one migration. New set_transit_status mirrors mark_arrived (members gate, can_participate, OPEN, arrival_window_open, blocked exclusion, CHILD-only guardian fan-out, audit). CRITICAL DELTA: idempotency must be PER-STATE — only ping on an actual state change, never re-ping the same state, never ping on a clear to NONE (caps a member at ~2 pings). Notification copy must be DERIVED from the transit state, not the generic 'Someone arrived' line (reuse mutable Kind.ARRIVAL with state-specific server-composed copy). Add transit_status to expire_arrivals bulk update + leave_activity reset list.

**Depends on:** mark_arrived pattern; Kind.ARRIVAL (mutable); expire_arrivals + leave_activity
**Touches:** apps/social/models.py; apps/social/services.py; apps/social/management/commands/expire_arrivals.py; apps/notifications/models.py; apps/web/views.py + templates

### F10 — Plan-B fallback meetup time  `[S/imp3/low/keep]`
*Theme: Reliability and showing up (calm, no shaming)*

**Pitch.** An organiser can pre-declare a single owner-curated fallback start time so a rained-out or quorum-short meetup gently shifts once instead of dying, and members are notified of the one backup slot rather than left guessing.

**Why it fits the invariants.** A property of THE MEETUP, never of any user (like min_to_go). Cohort/place/type stay locked, no new adult-minor path, no PII beyond one nullable timestamp, no tracking/vanity metric. Reuses the audited update_activity time-change path, so CHILD guardians in the member fan-out are re-notified and the read-time guardian manifest reflects the shift for free.

**Sketch.** Add nullable Activity.fallback_starts_at (mirrors min_to_go: field + migration + ActivityForm + ACTIVITY_EDITABLE_FIELDS). Add an owner-only, OPEN + before-start, idempotent invoke_fallback that requires fallback_starts_at set and strictly future, then routes through update_activity(starts_at=fallback_starts_at) — inheriting _supersede_reminders + member re-notify. THREE build requirements: (1) one-use latch — NULL fallback_starts_at inside the same transaction so it can't loop into open-ended reschedule; (2) reject when now is past fallback_starts_at; (3) the PR explicitly documents the pre-existing F7 latest_start_hour join-time-gate boundary rather than quietly adding a ward-eviction path.

**Depends on:** F1 min_to_go nullable-field precedent; F2 update_activity edit path + _supersede_reminders
**Touches:** apps/social/models.py; apps/social/services.py; apps/web/forms.py + views.py + templates; apps/social/migrations (one column)

### F11 — Quiet one-shot 'still coming?' RSVP nudge  `[S/imp3/low/keep]`
*Theme: Reliability and showing up (calm, no shaming)*

**Pitch.** One muteable, once-per-activity nudge — sent only inside the arrival window to members whose RSVP is still UNKNOWN — so the group gets an honest, last-minute headcount without shaming or repeated pestering.

**Why it fits the invariants.** At-most-once per (recipient, kind, url) via the same dedup as send_activity_reminders, auto-mutable through F31, no who-ignored count, no streak. Intent stays transient (resets to UNKNOWN on leave), so nothing is aggregated into per-user reliability history. Self-notification only, fixed copy — zero adult-minor contact path.

**Sketch.** New periodic command rsvp_finalize_nudge in DUE_JOBS (+ ALL_JOBS test). For each OPEN, non-hidden Activity inside arrival_window_open(), iterate voting_members(activity) — NOT current_members, so a seated supervisory GUARDIAN is excluded — whose attendance_intent == UNKNOWN. Send at most one notify() of a new mutable Kind RSVP_NUDGE deep-linking to the WEB activity detail page (where the RSVP control renders), NOT the API endpoint. Idempotent via the existing dedup. Add a WHY_REASONS entry + no-op makemigrations notifications.

**Depends on:** send_activity_reminders dedup template; arrival_window_open / voting_members / AttendanceIntent.UNKNOWN; DUE_JOBS + ALL_JOBS test; F31 mute gate; F1 quorum-go (consumer)
**Touches:** apps/social/management/commands/rsvp_finalize_nudge.py (new); apps/ops/management/commands/run_due_jobs.py + tests; apps/notifications/models.py + no-op migration; apps/social/tests

### F12 — Day-of jobs board (claim a task)  `[M/imp3/low/keep]`
*Theme: Organizer and facilitator tooling*

**Pitch.** An organizer posts a short fixed list of practical jobs for a meetup ('bring the ball', 'open the room', 'first-aid'), and members claim one — turning free-text 'what to bring' into coordinated, member-owned roles, with claim-visibility derived live (never a cross-activity reliability rollup).

**Why it fits the invariants.** Jobs live on a cohort-pinned, member-only Activity behind can_read_thread; the claim is a structured action on an organizer-defined CLOSED list — no claimant free text, so no new adult-minor channel. Counts stay job-level (claimed/open); a test pins that there is no per-user job-count rollup. The one free-text seam (the job label) is organizer-authored and rides the existing owner-text moderation path.

**Sketch.** New MeetupJob model: FK to Activity, short capped label, optional claimant FK SET_NULL. Services add_job (is_organizer-gated)/claim_job/release_job (current_members + can_participate, select_for_update on the job row for an atomic race-safe idempotent claim). CRITICAL refinement: resetting on leave_activity is necessary but NOT sufficient — eligibility can be lost without a REMOVED transition (consent/cohort/guardian revocation, suspension). So claimant display MUST be derived LIVE: render only if still in current_members AND can_participate (mirror is_supervisor_present), AND also NULL the FK on leave. record_audit on every state change.

**Depends on:** current_members/can_participate/can_read_thread/is_organizer; leave_activity transient-reset pattern; is_supervisor_present live-derivation precedent; owner-text moderation/report path
**Touches:** apps/social/models.py (+ migration); apps/social/services.py; apps/social/views.py + serializers.py; apps/web/views.py + activity_detail.html

### F13 — Reusable meetup blueprint ('create a similar meetup')  `[M/imp3/low/revise]`
*Theme: Organizer and facilitator tooling*

**Pitch.** An institutional organizer (librarian, coach, teacher) can start a new meetup from one they already ran — 'create a similar meetup' pre-fills the same format (title/description/difficulty/cost/accessibility/logistics), leaving place and date blank to choose fresh — without a new template model.

**Why it fits the invariants.** No place/cohort is carried over, so create_activity re-pins cohort=owner.cohort and re-runs the F9 child-venue + F25 public_places gates on the freshly-chosen place. Nothing stored beyond what the owner already authored on their own meetup; owner-scoped read only, no new PII/tracking/minor-contact/public-feed.

**Sketch.** REJECT the proposed new ActivityBlueprint model — it is a near-exact structural twin of the existing F4 ActivitySeries, which already stores the full format template (the identical field set). Instead add a MODEL-FREE prefill: activity_create accepts a validated, owner-scoped ?from=<activity_id> (or ?from_series=<id>) under the _visible_series_or_404 owner/staff guard, and seeds the form's editable text fields via setdefault — reusing the exact F36/F40 prefill block. PLACE and STARTS_AT are deliberately left blank (the gap over F4's fixed-cadence one-place spawn). create_activity's full gate stack re-runs on submit, so a prefill can never smuggle a CHILD meetup to an unapproved venue. setdefault never overwrites typed input. DRF parity returns the same seed dict.

**Depends on:** F4 ActivitySeries (the existing template store); create_activity full gate stack; F36/F40 setdefault prefill; owner-scope 404 guard
**Touches:** apps/social/services.py (seed-dict helper; no new model); apps/web/views.py; apps/social/serializers.py + views.py; apps/social/tests

### F14 — Per-instance 'heads-up for the next meetup' on a series  `[S/imp3/low/keep]`
*Theme: Organizer and facilitator tooling*

**Pitch.** A series organizer can stage a one-shot note that is appended to ONLY the next spawned instance's logistics ('back pitch this time, bring cleats'), then auto-cleared — timely per-occurrence guidance without waiting for the spawn or editing every instance.

**Why it fits the invariants.** Text-first owner-authored logistics onto an instance the owner already controls; spawn re-runs create_activity per instance so cohort isolation/consent/child-venue gating are untouched. organizer_note is already a member-visible logistics field — no new exposure. The consume-and-clear means the note never accrues into per-instance/per-user history.

**Sketch.** Add ONE nullable, max_length-capped next_instance_note to ActivitySeries (beside organizer_note). spawn_due_series reads it INSIDE the existing select_for_update(skip_locked=True) block and APPENDS it to the instance's organizer_note for that one spawn (append, not override — the standing template note must not silently vanish), then clears and persists via the same save(update_fields=[...]) — atomic, race-safe under the held row. A small owner-scoped set_next_instance_note mirroring pause_series/resume_series. LOAD-BEARING: enforce the cap at the model max_length AND form layer, because spawn runs with no request and won't re-validate the form.

**Depends on:** F4 ActivitySeries + spawn_due_series; existing organizer_note + per-instance spawn path
**Touches:** apps/social/models.py (+ migration); apps/social/services.py; apps/web/views.py + urls.py + forms.py + series_detail.html

### F15 — Single muteable 'plan the next one' completion nudge  `[M/imp3/low/keep]`
*Theme: Organizer and facilitator tooling*

**Pitch.** When a one-off meetup completes, its organizer alone gets one calm, F31-muteable nudge to re-run it — one tap pre-fills a fresh create form (or an F4 series) with the same type, place and logistics — plus a link to the easy-to-miss F22 'did we meet?' confirm.

**Why it fits the invariants.** The notice goes to activity.owner only (no fan-out, no adult-minor path); reuses create_activity's full gate stack so cohort/place/type stay pinned; the new Kind is mutable and routes through the F31 chokepoint; dedups on (recipient,kind,url) so it stores NO per-user completion history/metric/streak.

**Sketch.** The dominant completion path is auto_complete_activities, which uses a bulk .update() that BYPASSES complete_activity — so a notify() in the service would almost never fire. Extract a shared per-activity helper that BOTH complete_activity and the command call, firing ONE Notification(kind=MEETUP_FOLLOWUP) to the owner with the proven exists()-on-(recipient,kind,url) dedup. The url is the stable re-run target — activity_create with F40-style place/type/logistics params, NEVER a timestamp (so the cron can't re-notify). Extend the F40 prefill block to seed the 3 bounded logistics CharFields via setdefault. Add Kind + WHY_REASONS + no-op migration.

**Depends on:** F40 GET-prefill; F31 mute gate; F22 set_met_confirmed (link target); auto_complete_activities refactor to a shared helper; send_activity_reminders dedup
**Touches:** apps/social/services.py; apps/social/management/commands/auto_complete_activities.py; apps/notifications/models.py + migration; apps/web/views.py

### F16 — Guardian-set safe-arrival window backstop  `[M/imp3/med/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** A guardian opt-in 'should be there by now' backstop: setting an expected arrival window on a CHILD ward's specific meetup fires ONE gentle 'no arrival ping yet — you may want to check in' reminder to that guardian if the ward never self-marks arrival — no location, no tracking, opt-in per meetup.

**Why it fits the invariants.** CHILD-cohort-only, keyed strictly on an ACTIVE GuardianRelationship; the nudge goes to the GUARDIAN (no adult-minor path); stores no location and no presence record (derives from the existing ephemeral arrived_at + known activity times); ephemeral, never aggregated into ward 'reliability'.

**Sketch.** Reuse the GuardianGuardrail pattern but key a new ephemeral expectation on (GuardianRelationship, Activity) with CASCADE; store only an opt-in flag + small offsets relative to Activity.starts_at (sane fallback when ends_at is NULL). A new DUE_JOBS command checks at the window whether arrived_at is set; if not, fans ONE nudge via the send_activity_reminders dedup trick. THREE FIXES vs the naive pitch: (1) use a NEW MUTABLE Kind (e.g. WARD_OVERDUE), NOT the DSA-reserved non-mutable SYSTEM kind — muting is fine since the guardian opted in; (2) suppress on every wall the wards manifest enforces (cohort change, is_hidden, status!=OPEN, starts_at moved, revoked guardianship mid-window); (3) tune against alarm fatigue — soft 'you may want to check in' copy, conservative offset past start, fire at most once.

**Depends on:** ACTIVE GuardianRelationship + GuardianGuardrail per-relationship pattern; Membership.arrived_at + expire_arrivals + _supersede_reminders; DUE_JOBS registry; NEW MUTABLE Kind + dedup; wards view + form; record_audit
**Touches:** new GuardianArrivalExpectation model (CASCADE) + migration; apps/accounts/services.py; apps/social/management/commands/<new>.py + run_due_jobs.py; apps/notifications/models.py + migration; apps/web/views.py + wards.html; apps/safety record_audit

### F17 — Post-meetup safety check-in for child meetups  `[L/imp3/med/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** When a CHILD meetup completes, send each child member a calm 'was everything okay?' prompt with two one-tap options — 'all good' or 'something didn't feel right' — giving a reflective kid a low-stakes second chance to flag something, routed through a purpose-built post-meetup report (NOT the acute panic plumbing).

**Why it fits the invariants.** Extends the reporting funnel past the moment a meetup ends — complete_activity is silent today. The only fan-out is child -> own ACTIVE guardian via the proven _alert_guardians_unsafe (blocked-pairs excluded, SYSTEM non-mutable, no PII, no free text on the fast path). No adult-minor path; TEEN/ADULT stay silent.

**Sketch.** Two reshapes are required. (1) Do NOT reuse file_unsafe_report unchanged — its guardian copy is hardcoded 'used the I-feel-unsafe button DURING a meetup... check in NOW' and stamps the Report with the acute safe-exit sentinel, giving guardians a false panic alert and moderators false provenance. Add a sibling file_postmeetup_concern with its own sentinel + calmer copy, gated to status==COMPLETED. (2) The dominant completion path is auto_complete_activities (bulk .update(), never calls complete_activity), so a fan-out there misses every cron-completed meetup. Cleanest fix: emit the prompt LAZILY via a one-shot per-Membership checkin_prompted_at flag set when a child first opens a COMPLETED CHILD activity (mirrors the F39 welcomed_at idiom) — no cron fan-out. Copy MUST be genuinely calm so it never manufactures worry.

**Depends on:** _alert_guardians_unsafe mechanics; new Membership.checkin_prompted_at flag + migration; auto_complete_activities (bulk update — lazy prompt avoids touching it); SYSTEM Kind (reused); F11 triage queue (volume lands here)
**Touches:** apps/safety/services.py (new file_postmeetup_concern); apps/social/models.py + migration; apps/web/views.py (two-button action behind the activity_unsafe gate); apps/notifications/services.py

### F18 — Child-venue confidence chip + parent venue floor  `[M/imp3/low/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** Surface the existing F9 child-safe-venue verdict in the CHILD create flow (legibility before submit) and let a guardian set a per-ward 'vetted public venues only' floor that holds even if the deployment-wide flag is ever turned off.

**Why it fits the invariants.** Strictly pro-child: it only NARROWS a child's options, never loosens. The floor is a new field on GuardianGuardrail, enforced via effective_guardrail (keyed on an ACTIVE GuardianRelationship, audited). The chip is read-time derived from is_child_safe_venue (never written back), carries no PII, adds no visibility surface.

**Sketch.** TWO parts. (A) CHIP — the create form's place field is a flat ModelChoiceField with no per-option signal, so a 'live before submit' chip needs an explicit decision: (a) a small JS hook on the place picker, or (b) a no-JS path annotating each option / showing the verdict after a non-destructive submit. Pick one — do NOT pretend it's a one-line template add. (B) FLOOR — add child_safe_venues_only to GuardianGuardrail (+migration), fold into effective_guardrail (touches the F7 serializer-allowlist test), and enforce at ALL FOUR CHILD venue chokepoints (can_join, create_activity, create_series, F27 gauge) via a SINGLE shared helper child_venue_gate_active = settings flag OR effective_guardrail floor — wiring it into a subset re-creates the dead-end the code comments warn about. Pin a test that the floor can only ADD a rejection, never loosen.

**Depends on:** F9 is_child_safe_venue + CHILD_PUBLIC_VENUES_ONLY (four chokepoints); F7 GuardianGuardrail / effective_guardrail / wards UI; F15 read-time chip pattern
**Touches:** apps/accounts/models.py (+ migration); apps/accounts/services.py; apps/social/services.py (single helper through all 4 gates); apps/web/views.py + forms.py + templates; apps/accounts/tests + apps/social/tests

### F19 — Watch an activity ('tell me if it firms up')  `[M/imp3/low/revise]`
*Theme: Closing the find-people-and-go loop*

**Pitch.** A verified user can 'watch' one upcoming activity and get a single honest notice when it reaches a real 'firming up' milestone — quorum-go where set, else a member-count step or the approaching-start reminder — so the interested-but-unsure browser still ends up showing up.

**Why it fits the invariants.** A watch is a private, self-scoped opt-in row that is explicitly NOT a social.Membership, so it cannot feed connections.can_connect / shares_activity (F27 ActivityInterest is the exact test-pinned precedent). No watcher roster/count is ever stored or shown. Watch creation gates on visible_activities (cohort isolation); the notice reuses a mutable fan-out with blocked-pair exclusion.

**Sketch.** Add ActivityWatch(user, activity, unique pair, cohort pinned), watch/unwatch mirroring saved_searches, with a (user, activity) ledger so the notice fires at most once ever. KEY RESHAPE: min_to_go is nullable with no default, so the quorum latch (_maybe_confirm_meetup) NEVER fires for the common no-quorum activity — a watcher would wait forever. Fix the trigger so 'firms up' is honest for ALL activities: (1) fan to watchers from the quorum latch when min_to_go is set; (2) for non-quorum activities, fire on the approaching-start reminder fan-out (or a one-time member-count step) through the same at-most-once ledger. The watch-button copy must state exactly which notice the user will get so it never over-promises. Self-expire once status != OPEN.

**Depends on:** F1 quorum-go latch (insufficient alone — min_to_go optional); approaching-start reminder fan-out (all-activities trigger); F3 saved_searches opt-in + at-most-once ledger; notify() mute gate + WHY_REASONS; visible_activities + blocked_user_ids
**Touches:** apps/social (new ActivityWatch + ledger); apps/social/services.py (two-trigger fan-out); apps/notifications (new mutable Kind + migration); apps/web (watch button + /you/ list)

### F20 — Crowd accessibility facts for venues OSM is silent on  `[L/imp3/med/revise]`
*Theme: Place and event data quality*

**Pitch.** Let verified members confirm/dispute step-free / accessible-toilet / hearing-loop facts via the existing F19 venue-fact vote, filling the OSM gaps that make the access badge near-useless — OSM stays authoritative, crowd only fills silence.

**Why it fits the invariants.** Rides the proven F19 gate: verified+consented voters only, counts-only with no voter list and no free text, an ingest-safe own table, can_participate-gated, rate-limited. The model docstring already pins that co-voting a place is NOT a shared activity, so can_connect is untouched. Venue facts are cohort-agnostic physical-place data — no PII, no contact surface.

**Sketch.** Add FactKeys STEP_FREE/ACCESSIBLE_TOILET/HEARING_LOOP + three _FACT_OSM kv entries (wheelchair, toilets:wheelchair, hearing_loop); vote_on_fact/place_fact_status/_crowd_state are reused verbatim (choices-only migration). TWO reconciliations the naive sketch missed: (1) STEP_FREE has an OSM 'limited' state _crowd_state can't reproduce — keep _tristate(allow_limited=True) for the wheelchair tag and ONLY fall through to crowd when OSM is 'unknown' (never overwrite a real OSM 'limited' with a binary crowd majority). (2) THE N+1 — accessibility_facts() is a free dict-read called per-place across four hot list surfaces; routing it through .count() fans out to hundreds of queries. A query-backed path MUST prefetch the tally exactly like F28's recent_report_n annotation.

**Depends on:** F19 PlaceFactVote overlay (vote_on_fact / place_fact_status / _crowd_state); F15/F32 accessibility facts + access-match badge/sort; F28 recent_report_n prefetch pattern (avoid the N+1); can_participate + allow_action
**Touches:** apps/places/models.py (+3 keys); apps/places/services.py (OSM-first-then-crowd-fill + a batched/prefetched tally variant); apps/web/views.py; apps/discovery/services.py + apps/recommendations/services.py; apps/web/templates/web/place_detail.html

### F21 — Hours-correction overlay (fix the posted hours, not just flag them)  `[S/imp3/low/keep]`
*Theme: Place and event data quality*

**Pitch.** Let members propose the correct opening hours for a venue behind the same quorum as name/address corrections, validated through the existing parser, so a venue stops being permanently 'unknown' the moment someone records the real hours.

**Why it fits the invariants.** Place metadata is cohort-agnostic with no contact path. The proposed value is gated through parse_opening_hours() — which rejects anything that isn't days + HH:MM ranges — so the 255-char string can't serve as a covert free-text channel. Applied at read time via a display property, NEVER written back to Place, so re-ingest can't clobber it and canonical OSM is never poisoned. Counts-only pending UI inherited from F20.

**Sketch.** Add HOURS to PlaceCorrection.Field + widen the validation message and the place_detail <select>. Reuse propose/confirm/publish/reject + N-confirmer quorum + proposer-exclusion + counts-only UI UNCHANGED. Field-specific branch in propose_place_correction: when field==HOURS, run parse_opening_hours(value) and reject if None. CRUX the naive sketch glossed: open_now_status()/is_open_at() consume the PARSED JSON field, not the raw string — so open_now_status must call parse_opening_hours() on a published correction and feed THAT to is_open_at, never re-parse place.opening_hours. ONE policy line to decide: existing F28 OpenNowReports were filed against the OLD hours and should auto-clear/decay after a correction so a venue isn't simultaneously 'corrected' and 'unverified'.

**Depends on:** F20 place-corrections overlay; F28 open-now reports (the read path this overlays); parse_opening_hours / is_open_at
**Touches:** apps/places/models.py (HOURS field + display property); apps/places/services.py; apps/web/templates/web/place_detail.html; apps/places/serializers.py

### F22 — More crowd venue facts: getting-there  `[S/imp3/low/revise]`
*Theme: Place and event data quality*

**Pitch.** Add car-free 'how do I actually get there' facts (bike parking, car parking, bus/tram nearby) to the existing OSM-first crowd venue-facts overlay, so minors and non-drivers can judge a venue from more than an address pin.

**Why it fits the invariants.** Rides the already-hardened F19 overlay: counts-only, identity-blind, closed-allowlist boolean (no covert free-text), ingest-safe, can_participate-gated, rate-limited, one-row-per-(place,user,key). Voting on a venue is explicitly NOT a shared activity and never enables can_connect. No PII, no public feed, no engagement mechanics. Venue metadata is cohort-agnostic.

**Sketch.** Extend PlaceFactVote.FactKey + _FACT_OSM: BIKE_PARKING and CAR_PARKING map straight onto the existing ('present',(k,v)) spec with zero new code path; BUS_TRAM_NEARBY is a crowd-only entry like INDOOR_SHELTER. All three flow UNCHANGED through place_fact_status / venue_facts / vote_on_fact (every reader iterates the FactKey enum) and render with no template change. A choices-only makemigrations places. EXPLICITLY DEFER the prefers_quiet sort-wiring to its own feature — it would be a real N+1 across three hot sort paths and would break the documented test_prefers_quiet_alone_never_reorders invariant; it delivers nothing at Cluj cold-start. Ship only the getting-there facts here.

**Depends on:** F19 PlaceFactVote overlay (verbatim reuse)
**Touches:** apps/places/models.py (FactKey); apps/places/services.py (_FACT_OSM); apps/places/migrations (choices-only); apps/places/tests

### F23 — Seasonal & public-holiday opening hours  `[M/imp3/low/keep]`
*Theme: Place and event data quality*

**Pitch.** Stop telling members a park/library is 'open' on a Romanian public holiday or out-of-season when the OSM hours string carries a PH/seasonal rule the parser currently drops on the floor.

**Why it fits the invariants.** A confident-but-wrong 'open' sends someone to a locked gate, which is worse than 'unknown'. No PII (a public-holiday calendar isn't personal data), no child-safety surface, no adult-minor path, no tracking. Pure-Python, Postgres-primary, no new heavy deps; preserves the fail-closed contract (ambiguous parse -> None 'unknown', never a false 'open').

**Sketch.** Extend opening_hours.py: grow the parsed form with a PH bucket + optional season/date-range scoping, parse the PH token and simple 'Apr-Oct' qualifiers, and have is_open_at() consult the calendar DATE (it ALREADY receives a full datetime and callers pass timezone.localtime() — NO signature change needed, only the body). A small pure-Python RO holiday module supplies fixed-date holidays + Orthodox Easter via dateutil.easter(year, EASTER_ORTHODOX) (python-dateutil is already pinned — do NOT hand-roll the Julian offset). CORRECTION to drop from framing: the parsed JSON is STORED on Place.opening_hours at ingest, NOT a read-time overlay; that's fine (deterministic from the raw string) but the 'read-time derived' claim is inaccurate.

**Depends on:** opening_hours.py parser + is_open_at; open_now_status consumer (unchanged); python-dateutil (already pinned); complementary to F28 (not blocking)
**Touches:** apps/places/enrichment/opening_hours.py; apps/places/enrichment/ (new RO holiday module); apps/places/tests/test_opening_hours.py

### F24 — Cross-source event de-dup + feed-cancellation signal  `[M/imp3/low/revise]`
*Theme: Place and event data quality*

**Pitch.** Reflect a venue's own iCal 'cancelled / dropped from feed' status automatically so the feed stops advertising low-traffic (often kids') meetups that aren't happening, instead of waiting for an F21 crowd quorum that may never form.

**Why it fits the invariants.** feed_status is operator/feed-sourced, NOT user-writable (no brigading lever); events are AllowAny + cohort-blind (no minor-adult path); it is a read-time REMOVAL signal (the opposite of engagement-maxxing); no PII, no photo surface. Composes with — never replaces — the F21 crowd/decay overlay.

**Sketch.** Two halves with sharply different time-to-value — SPLIT and ship feed_status first. (1) feed_status [NOW]: add Event.feed_status (LIVE/CANCELLED/STALE) + migration; parse STATUS:CANCELLED in parse_ics; mark events that vanished from a fully-successful per-feed sync as STALE (ONLY after a clean fetch, so a partial fetch never hides live events). CORRECTNESS FIX the pitch got wrong: read surfaces do NOT uniformly drop F21-flagged events today — HappeningView filters but the web events_list does not. Drop CANCELLED/STALE inside upcoming_events() itself (closing that existing inconsistency) AND add the exclude explicitly to HappeningView's inline queryset. (2) Event dedup [DEFER to the Phase-2 second source]: one iCal class today = no duplicates, so it delivers ~nothing now.

**Depends on:** parse_ics + safe_get fetch; upcoming_events / events_with_public_places (single chokepoint for the drop); HappeningView inline queryset (needs the exclude too); Phase-2 second event-source adapter (dedup half only)
**Touches:** apps/events/sources.py; apps/events/services.py; apps/events/models.py (+ migration); apps/events/management/commands/sync_event_feeds.py; apps/discovery/views.py

### F25 — Volunteering & community-good activity branch  `[S/imp3/low/keep]`
*Theme: Real-world and civic impact + sustainability*

**Pitch.** Add a first-class 'Volunteering / community good' activity branch (park cleanup, library shelving, repair-cafe, community-garden) — additive taxonomy data + OSM tag rules — so people can find real-world good to do together, not just sport; adults at launch, minors only after a deliberate per-venue-class safeguarding decision.

**Why it fits the invariants.** Pure additive data on proven seams: no schema change, no new contact path, no PII, no public-feed/photo surface, no tracking. Cohort isolation, the F9 child-venue allowlist, F7 guardrails and the supervisor seat all apply unchanged and fail-closed, so it cannot create an adult-minor path. Postgres-primary, no new deps.

**Sketch.** (1) A taxonomy data migration cloning the reading/archives seed: a new ActivityCategory 'volunteering' + ActivityType rows + ActivityRelation links (no schema change). (2) TagRule entries appended to the flat MAPPING list for social_facility / community-garden / recycling-style tags. (3) Optional ?category= narrowing on ActivitiesFeedView (one join branch). Activities flow through create_activity / visible_activities / recommendations unchanged. CRITICAL SCOPING: ship ADULTS-ONLY. Do NOT seed volunteering ChildVenueClass rows — public_child_venue_class returns 'unknown' so CHILD volunteering is correctly fail-closed OUT. Minor volunteering at venues serving the vulnerable public (food banks, shelters) is a distinct safeguarding decision needing its own policy review and likely a supervisor-seat default.

**Depends on:** taxonomy ActivityCategory + ActivityType (no schema change); ingestion/mapping.py flat MAPPING list; discovery ActivitiesFeedView (?category= join branch); ChildVenueClass DELIBERATELY left unseeded (fail-closed for CHILD)
**Touches:** apps/taxonomy/migrations/ (new data migration); apps/ingestion/mapping.py; apps/discovery/views.py; apps/web/ (optional category nav)

### F26 — Spend-tied campaign close-out (the honest loop)  `[S/imp3/low/keep]`
*Theme: Real-world and civic impact + sustainability*

**Pitch.** When staff close an earmarked campaign, they publish a calm one-line plain-text outcome plus any linked spend rows, so donors can see what their gift actually funded.

**Why it fits the invariants.** Closes the repeat-giving trust loop on the donation-only model. Staff-only admin write, aggregate-only read (no donor PII, mirroring F29/F34), no minor-facing surface, no contact path, no public photo/feed, no behavioural tracking. Donations is a flat cohort-free app, so no cohort wall to subtly breach.

**Sketch.** Add to Campaign a nullable capped outcome TextField (280 chars, |linebreaks + autoescape) + a nullable closed_at — both set ONLY in CampaignAdmin (the existing F34 staff write path). Add an optional SpendEntry.campaign FK (SET_NULL, a verbatim copy of the Donation.campaign pattern) so an untagged spend row still tallies globally but won't appear under the outcome (correct fail-safe — never a false 'delivered' claim). Add completed_campaigns_with_outcomes() returning plain aggregate dicts (one grouped query, no N+1, no donor objects). ETHOS GUARD: the section must read as a neutral ledger close-out, NEVER 'we hit our goal!' with any bar/animation. The outcome text is load-bearing; linked spend rows must not gate the section.

**Depends on:** F34 earmarked campaigns (Campaign + active_campaigns_with_progress + /campaigns/ template); F29 spend transparency (SpendEntry + spend_by_category)
**Touches:** apps/donations/models.py (+ migration); apps/donations/services.py; apps/donations/admin.py; apps/web/views.py + campaigns.html

### F27 — Read-aloud-friendly plain-language meetup brief  `[S/imp3/low/keep]`
*Theme: Accessibility and inclusion*

**Pitch.** A deterministic, template-only one-region summary of an activity ('What, where, when, what to bring, getting home') in short labelled declarative sentences from already-stored fields — optimised for screen readers, low-literacy, and elderly users — gated to exactly the same visibility as the fields it draws from.

**Why it fits the invariants.** Pure read-time string assembly: no ML, no new PII, no write path, no model/migration, no public-feed/photo surface, no tracking. The only safety-relevant requirement is mirroring the established field-visibility split (cohort-visible chips vs member-only logistics) and emitting NO numeric counts to anyone — which keeps child safety intact.

**Sketch.** Add social.services.plain_meetup_brief(activity, *, is_member) beside thread_digest/draft_activity_text — a pure-string composer returning an ORDERED list of (label, sentence) pairs. Always include title, type, place, time + the cohort-visible chips (cost_band/difficulty/accessibility_notes). Include member-only logistics (meeting_point/what_to_bring/organizer_note/getting_home_note/first_time_note) ONLY when is_member — reusing the SAME is_member signal the view already computes, never re-deriving membership. Emit NO numeric counts at all (avoids re-implementing thread_digest's minor-suppression and can't leak counts to minors). Render as a single ARIA-landmarked region at the top of activity_detail.html, no JS. The non-member visibility test is the key gate.

**Depends on:** F35 thread_digest composer + viewer-gating precedent; F36 draft_activity_text composer precedent; F8/F9/F18/F41 source fields; activity_detail view (already passes activity/is_member)
**Touches:** apps/social/services.py; apps/social/tests/; apps/web/views.py; apps/web/templates/web/activity_detail.html; locale RO/EN strings

### F28 — Guardian-assisted interest & access setup  `[M/imp3/low/keep]`
*Theme: Accessibility and inclusion*

**Pitch.** Let an already-linked guardian seed their CHILD ward's declared interests and AccessPreference on the ward's behalf, so the youngest verified members aren't stranded on an empty soonest-first feed they can't personalize alone.

**Why it fits the invariants.** Sets PREFERENCES only — no messaging/contact channel, no widening of participation access — so it cannot create an adult-to-minor discovery or contact path. Interests feed only cohort-isolated recommendations and the same-cohort avatar; communities derive from place/type predicates, not interests. No new PII (the models exist); every write audited + rate-limited like the F7 guardrail path, and honestly reflected in BOTH F13 legibility panels so the ward transparently sees a guardian set these.

**Sketch.** Clone the F7 set_guardian_guardrail write-on-behalf pattern verbatim. Add two guardian-gated wrappers: set_ward_interests + set_ward_access_preference, each (1) resolving the ACTIVE GuardianRelationship via select_for_update, (2) hard-rejecting a non-CHILD ward (mirror the 'teens self-manage' convention), (3) delegating to the existing set_interests / set_access_preference, (4) record_audit(actor=guardian, target=ward) in-txn. Surface can_help_set_interests/access in guardianship_capabilities + the current seeded values so both panels render what was set. DECIDE-AND-DOCUMENT the CHILD-only (recommended) vs CHILD+TEEN gate explicitly. Note: the F17 'matches your interest in X' reason becomes literally true for a guardian-declared interest — fine given two-sided F13 legibility, but the panel should say so.

**Depends on:** guardianship_capabilities + ACTIVE-GuardianRelationship gate; F7 set_guardian_guardrail write-on-behalf pattern; recommendations.set_interests + places.set_access_preference; F13 two-sided panels; record_audit + allow_action
**Touches:** apps/accounts/services.py; apps/web/views.py + wards.html + guardianship.html; apps/recommendations/services.py (reused); apps/places/services.py (reused); tests

### F29 — Pro-safety call-off helper for outdoor meetups  `[S/imp2/low/revise]`
*Theme: Reliability and showing up (calm, no shaming)*

**Pitch.** When an organiser calls off a weather-exposed outdoor meetup, the cancel flow offers fixed safety-reason chips (weather / unsafe conditions / low numbers) and composes the member notice from calm fixed copy — so calling it off reads as the responsible choice, not a guilt-laden bail.

**Why it fits the invariants.** Lowers the social cost of aborting a genuinely unsafe hike/run/ride — the one place the in-person mission carries physical risk. No new contact path, no PII, no behavioural tracking; stores NO per-user cancellation history (the reason code lives on the Activity, set at cancel time only). Reuses the single owner-gated cancel_activity service and the existing member-notify (incl. CHILD guardian fan-out).

**Sketch.** Add an optional fixed-choice cancel_reason_code (WEATHER/UNSAFE/LOW_NUMBERS/OTHER, nullable TextChoices) to Activity, set only inside cancel_activity. When a code is supplied, compose the notice body from fixed i18n copy; OTHER falls back to the existing free-text reason. KEY RESHAPE: do NOT detect 'outdoor' by category.slug=='outdoor' — that one-hop check misses climbing/bouldering (under fitness), beach volleyball (team_sport), open-air cinema/festival (culture). Add a reusable ActivityType.weather_exposed boolean (mirrors the existing wellness/family_friendly flag pattern) + seed migration, and show the weather/unsafe chips only when activity.activity_type.weather_exposed. DROP the 'plan-B fallback' from scope (that's a separate reschedule feature off update_activity).

**Depends on:** cancel_activity (owner-gate, OPEN-only, member-notify, audit — exists); notify() choke point + ACTIVITY_UPDATED-style copy; ActivityType wellness/family_friendly flag pattern (to mirror)
**Touches:** apps/social/models.py (cancel_reason_code + migration); apps/social/services.py; apps/taxonomy/models.py (weather_exposed flag + seed migration); apps/web/templates/web/activity_detail.html + views.py; i18n RO/EN

### F30 — Courtesy heads-up on a last-minute drop  `[S/imp2/low/keep]`
*Theme: Reliability and showing up (calm, no shaming)*

**Pitch.** When a member leaves an OPEN meetup inside a pre-start window, send ONE neutral roster-change heads-up to the organiser only — fixed copy, no reason field, no leaver free text — so a near-quorum meetup learns it slipped below min_to_go in time to react, without ever building a per-user no-show record.

**Why it fits the invariants.** Owner-only fan-out: the owner is always the same cohort as their own activity and already sees roster departures, so NO adult-minor path opens. leave_activity already zeroes attendance_intent + met_confirmed_at, and the codebase forbids any per-user reliability field — so as long as nothing is aggregated and the copy stays neutral (a roster change, not 'X bailed'), the no-shaming rule holds.

**Sketch.** Add an optional courtesy flag (default OFF) + a configurable pre-start window to leave_activity. When a MEMBER (role != GUARDIAN) leaves an OPEN activity inside the window, fire ONE notice to the OWNER only, REUSING Notification.Kind.ACTIVITY_UPDATED (no new kind, no migration) so it can't read as a personalised 'member dropped out' event — neutral copy like 'Someone left an activity you organise; check who's still going.' NO reason field, NO leaver free text, NO new per-user model field/count: the notice is transient. Gate skips supervisory-guardian leaves. TWO conditions: reuse ACTIVITY_UPDATED (never add a MEMBER_DROPPED kind), and default OFF + never persist anything per-leaver. Build only after confirming product wants the push given the existing live remaining-needed chip.

**Depends on:** F1 quorum-go (min_to_go / attendance_summary); notify() + ACTIVITY_UPDATED (no new kind); activity_leave POST view; leave_activity (already resets intent; voting_members excludes GUARDIAN)
**Touches:** apps/social/services.py; apps/web/views.py; apps/web templates; tests

### F31 — Low-bandwidth / data-saver lite mode  `[M/imp3/low/keep]`
*Theme: Accessibility and inclusion*

**Pitch.** A persisted, non-tracking 'lite mode' functional cookie that drops Leaflet+OSM tiles, the avatar SVGs, the live WebSocket and the service worker, falling back to the already-shipped JS-free text equivalents — for metered data and low-end Android phones in the launch city.

**Why it fits the invariants.** Serves the cheap/lean civic-audience mission and only REMOVES surfaces, degrading to paths that already exist and were already reviewed (F16 /places/list/, the native compose-form POST, F38's JS-gated SW). Stores a single binary functional cookie matching the F12 allowlist pattern (samesite=Lax, no PII) — no behavioural/device/connection inference. No new endpoint, model, or read path means no new child-safety or feed surface.

**Sketch.** Add a fourth allowlisted functional cookie (display_lite) alongside the F12 theme/text/motion cookies; write it through the EXISTING display_preferences view/form (NOT settings_hub, which is a pure links page — that only needs a link). Then guard with the flag: (1) skip the F38 serviceWorker registration; (2) places.html links to /places/list/ instead of the Leaflet head; (3) activity_form.html skips the Leaflet include (plain dropdown stands alone); (4) activity_detail.html skips the thread WebSocket (compose form POSTs natively, which it already supports); (5) avatar sites render a text initial. HONEST CAVEAT: the avatar is an inline data-URI, so skipping it is a CPU/render win, NOT bytes — the real byte savings are Leaflet (unpkg CDN) + OSM tiles + the socket.

**Depends on:** F12 display-preference cookie pattern + display_preferences view; F16 JS-free /places/list/; activity_detail native compose-form POST fallback; F38 JS-gated SW block; avatar_uri templatetag
**Touches:** apps/web/context_processors.py; apps/web/views.py; templates/base.html; apps/web/templates/web/places.html + activity_form.html + activity_detail.html; avatar templates

### F32 — Your words in your takeout (Art.20 portability for thread content)  `[S/imp3/low/keep]`
*Theme: Privacy and data-dignity as product*

**Pitch.** Add the user's OWN authored thread posts and announcements to the GDPR Art.20 export so their actual words travel with them, not just the metadata.

**Why it fits the invariants.** build_user_export today discloses everything ABOUT a member but not the personal content they authored — the most personal data they create here. Self-scoped and read-only: you export only your own text, never a contact path, no tracking, no public feed, no media bytes. Postgres-only, no new dep.

**Sketch.** Add a private _thread_posts(user) helper + one line in build_user_export. Walk Post.objects.filter(author=user).select_related('thread__activity','thread__group') with an id cap ([:5000]). Project to a STRICT allowlist: body, created_at, is_edited (DERIVED via updated_at>created_at — do NOT add a DB field), is_announcement, and the parent thread's title+id via the activity-XOR-group bridge. HARD EXCLUSIONS: never the reply_to PARENT's body or the derived reply_snippet (another member's words); never the shared_activity/place/event target content; never attachment bytes (note only 'had attachment'). Include the author's own is_hidden post body with a neutral 'removed' marker — never a moderator identity. Tests: own posts appear, another member's reply/parent body never appears, the cap holds.

**Touches:** apps/accounts/export.py; apps/accounts/tests

### F33 — Erasure preview — exactly what deleting removes (and what stays)  `[S/imp3/low/keep]`
*Theme: Privacy and data-dignity as product*

**Pitch.** Before the irreversible self-delete, show an honest counts-only inventory of what erase_user destroys and the one audit pseudonym that lawfully survives — so 'right to be forgotten' stops being a black box.

**Why it fits the invariants.** Makes the one-way erasure legible without leaking anything new: counts-only, no content rendered, strictly self-scoped. It ADDS a confirmation step rather than removing one (the opposite of a retention dark pattern). The honesty about the surviving UUID-pseudonym audit row preempts a 'you said you deleted everything' trust/DSA complaint — the standout dignity win.

**Sketch.** Add erasure_preview(actor, target) reusing the SAME ORM relations erase_user cascades over (the build_user_export gatherer pattern) but returning .count() values only — no titles, no content. Guard with erase_user's own actor==target or is_guardian_of check. Add a fixed accurate 'what stays' note (account/blobs/owned activities+groups/E2EE ciphertext destroyed; one audit entry survives with only a UUID pseudonym + actor_ref, never the username). Wire as a GET confirmation step on account_delete — this also FIXES the currently-broken my_privacy.html link that GETs a @require_POST endpoint and 405s; POST still does the irreversible erase. SCOPE THE FLAGSHIP TO SELF-DELETE (the guardian ward-preview has no web surface today — a separate sensitive view, optional follow-on). Add a divergence test pinning preview counts to what erase_user actually cascades.

**Depends on:** erase_user cascade (preview mirrors it — pinned by a divergence test); build_user_export gatherer; record_audit account.erased ('what stays' note); is_guardian_of guard
**Touches:** apps/accounts/services.py (erasure_preview); apps/web/views.py account_delete (GET-preview / POST-confirm); apps/web/templates/web/ (delete-confirm template) + my_privacy.html; apps/web/tests

### F34 — 'Who can see this' legibility on the composer & thread  `[S/imp3/low/keep]`
*Theme: Privacy and data-dignity as product*

**Pitch.** A calm, honest 'visible to: members of this activity — never public, never to other cohorts; a guardian observer may be reading' line on the composer and thread header, so the audience boundary is felt before you type.

**Why it fits the invariants.** Converts invisible scope gates into felt assurance at the moment of typing, without adding any capability. Read-only and additive — stores nothing, changes no gate, opens no adult-to-minor path, no public/photo surface, no tracking, no PII (presence is a boolean; no names). It only re-describes gates that already hold via the same functions that enforce them.

**Sketch.** Add a pure-read thread_audience_summary(viewer, owner_obj). Reuse thread_members + can_read_thread for the audience scope; reuse the thread_digest count-suppression rule (numeric block is ADULT-only, None for CHILD/TEEN) for the peer-reader count, returning a generic 'your group' phrase to minors. For the guardian-observer line, do NOT use active_supervisor_present — it is keyed on a guardian OF THE OWNER (F29 supervision) and would FALSELY report 'no guardian reading' when a non-owner child's own seated guardian is in fact reading. Derive it from 'any seated GUARDIAN-role membership exists' on the owner_obj, which mirrors the actual thread read-access set. Emit explicit negatives ('never public, never indexed, never shown to other cohorts'). Tests: minors get the generic phrase + the guardian line is true iff a guardian-role member is seated.

**Depends on:** thread_members + can_read_thread; thread_digest count-suppression rule; seated GUARDIAN-role membership (NOT active_supervisor_present); PostForm composer (shared Activity+Group)
**Touches:** apps/social/services.py; apps/web/forms.py; apps/web/templates/web/activity_detail.html; i18n RO/EN

### F35 — 'Where your data goes' processors & residency panel  `[S/imp3/low/keep]`
*Theme: Privacy and data-dignity as product*

**Pitch.** A read-only, config-derived 'data recipients' panel naming every external party that can touch user data (EUDI age-proof issuer, Stripe donations, S3 storage region) with honest purpose/data-category/residency notes — surfacing GDPR Art.13/14 transparency as product, never per-user.

**Why it fits the invariants.** Pure config read — no PII, no per-user data, no model, no migration. Cannot create an adult-minor path, cannot track, adds no photo/feed surface. It is positive negative-space ('no ad networks, no analytics processors') — the exact opposite of the tracking risks. Strengthens the EU-compliance invariant rather than threatening it.

**Sketch.** Add one read-only data_recipients() helper introspecting the existing seams (get_payment_provider().name, trusted_issuers(), get_storage()/MEDIA_S3_REGION, render.yaml region:frankfurt) and emitting a static plain-language list (who / purpose / data category / residency note) + a /data-recipients/ route mirroring /my-privacy/ and /transparency/. NOT a duplicate (privacy.html names zero third parties; /my-privacy/ is self-scoped 'what we know about YOU'). CRITICAL HONESTY CONSTRAINT: state only what config PROVES — 'Stripe processes card payments on its hosted page (we never receive card data)', NOT 'your data stays in the EU' (Stripe is global; Frankfurt is a Render config fact, not a contractual guarantee). Gate the copy on the Phase-2 DPIA sign-off so the page doesn't itself become a GDPR misstatement.

**Depends on:** donations get_payment_provider / .name; eudi trusted_issuers; media get_storage / MEDIA_S3_REGION; render.yaml residency facts; Phase-2 DPIA sign-off to vet wording before launch
**Touches:** apps/web/views.py + urls.py; apps/web/templates/web/ (new template); read-only reads of providers.py / trust.py / storage.py / render.yaml

### F36 — Self-service printable donation receipt  `[S/imp3/low/revise]`
*Theme: Real-world and civic impact + sustainability*

**Pitch.** Give every donor a login-required, self-scoped print-friendly receipt for tax/employer-match paperwork; defer the recurring-gift management page until a real subscription provider exists.

**Why it fits the invariants.** Serves the donation-only sustainability mission by unblocking EU donors who need paperwork, and actively upholds the no-dark-patterns rule by being explicitly anti-upsell (no guilt/retention screen). Touches no child-safety or in-person surface, creates no adult-minor path, stores no new PII or card data (opaque external_ref only), adds no public feed/photo, needs no heavy/ML deps (browser print-to-PDF).

**Sketch.** SHIP NOW: a @login_required receipt(request, donation_id) view + URL filtering STRICTLY donation = Donation.objects.get(donor=request.user, pk=...) (mirror MyDonationsView's self-only filter to avoid IDOR), rendering a print-friendly HTML receipt (org name from settings, amount via |cents, date, opaque ref, EUR, status). Link from each my_donations card. No new model, migration, provider change, or PDF dependency. DEFER: the RecurringPledge model + cancel_pledge service — the Stripe provider today is mode=payment one-off only and complete_donation reconciles purely by external_ref, so there are no real recurring charges to show or cancel; building that now yields dead code and a misleading 'cancel your recurring gift' UI. Revisit when Phase-2 Stripe subscriptions land.

**Depends on:** my_donations view + template (self-only filter to mirror); Donation model fields (all present); |cents templatetag; DEFERRED: Phase-2 Stripe subscriptions before any RecurringPledge lifecycle
**Touches:** apps/web/views.py + urls.py; apps/web/templates/web/receipt.html (new); apps/web/templates/web/my_donations.html; apps/web/tests (self-scope/IDOR)

### F37 — Honest taxonomy bridge for thin / cold-start feeds  `[M/imp2/low/revise]`
*Theme: Closing the find-people-and-go loop*

**Pitch.** When the interest feed yields fewer than `limit` items, fill the tail with activities one lateral ActivityRelation RELATED hop from a declared interest, labelled with the true bridge reason ('related to your interest in Running') — never a fabricated % match.

**Why it fits the invariants.** Derived from the STATIC taxonomy graph + the viewer's OWN declared interests (no behavioural signal). Routes through visible_activities (cohort isolation + blocking + status/time gates hold identically). Adds no embedding/pgvector/cloud-AI cost. The label stays literally true (mirrors the F17 honest-reason discipline). Points only at real, in-person, OPEN meetups. No new model, migration, PII, or surface.

**Sketch.** Scope the fill to ONLY the lateral ActivityRelation RELATED edges — DROP the 'is-a sibling / shared category' half, which is redundant: embeddings.py already hashes cat:<ancestor> tokens, so a category-sibling activity already earns a positive cosine score and surfaces with an honest % match. In recommend_activities, if len(activities) < limit: expand the viewer's declared-interest type ids by one symmetric RELATED hop, then fill from visible_activities(OPEN, future, those types, excluding already-recommended/joined) up to the remaining slots, BELOW the true matches. In recommended_with_reasons, special-case bridge items: rec_reason = 'related to your interest in X' and DO NOT set match_pct (a bridge item has no cosine score, so it must never borrow the % string).

**Depends on:** recommend_activities + recommended_with_reasons; ActivityRelation RELATED edges (is-a siblings dropped as redundant); visible_activities + with_counts (route the fill through these); embeddings.py cat: tokens (verify-only: already encode category adjacency)
**Touches:** apps/recommendations/services.py; apps/recommendations/tests/; apps/web/tests/

### F38 — Kid-trusted report-status follow-up  `[S/imp2/low/revise]`
*Theme: Deepening the child-safety promise*

**Pitch.** Make the ALREADY-EXISTING report-outcome loop legible to the youngest users: deep-link the resolution notice to /my-safety-record/ and soften the raw status enums into child-readable, still-truthful labels.

**Why it fits the invariants.** A one-way SYSTEM notice plus a strictly self-scoped read page — no moderator identity, no other party's data, no contact path, no tracking. The one invariant to guard is DSA Art.16 honesty: softened copy for a DISMISSED report must never imply action was taken.

**Sketch.** The original premise is FALSE against the code — the SYSTEM outcome-notice loop (take_action/dismiss_report fire 'reviewed... took action' / '...no action was needed') and the self-scoped per-report status projection (safety_record_for, including status_label/handled_at/resolution at /my-safety-record/) ALREADY ship under F19. The ONLY genuine residual is cosmetic trust polish for under-16 users: (1) deep-link — _notify_reporter doesn't forward a url though notify() already accepts url=; pass reverse('safety_record') from the two callers; (2) child-readable copy — map the raw enum labels ('Open'/'Reviewing'/'Actioned'/'Dismissed') to softer truthful copy ('being looked at'/'resolved'). DROP the false 'no feedback loop today' framing. No new model, migration, channel, gate, or allowlist change. Ship bundled with other child-trust copy work, not as a headline feature.

**Depends on:** _notify_reporter / take_action / dismiss_report (existing Art.16 notices); safety_record_for (existing self-scoped projection); notify() url= param (already exists); /my-safety-record/ view + template
**Touches:** apps/safety/services.py (_notify_reporter add optional url; soften status_label); apps/web/templates/web/safety_record.html

### F39 — Partner-stewarded venue credit on activities & the JS-free places list  `[S/imp2/low/revise]`
*Theme: Real-world and civic impact + sustainability*

**Pitch.** Show a truthful read-time 'At a venue stewarded by <partner>' credit on activities at a verified civic partner's venue, and mirror F37's one-line credit onto the JS-free /places/list/ rows — extending civic recognition parity to the accessibility-first surface.

**Why it fits the invariants.** Pure read-time acknowledgement of staff-curated, text-only public partner facts — no user data, no cohort variation, no minor-exposure path, structurally incapable of becoming an ad surface (Partner has no logo/rank/boost field). Visibility is gated through the existing Partner.objects.public() chokepoint, so a deactivated/unverified partner silently drops everywhere.

**Sketch.** DROP the proposed host_partner FK and any 'Hosted by' wording. partner_for_place(place) already derives + renders the credit on place_detail. Reuse it: pass partner=partner_for_place(a.place) into the activity card/detail context and render a cohort-visible chip worded as PROVENANCE — 'At a venue stewarded by <name>', NOT 'Hosted by' (a self-asserted 'Hosted by <library>' chip is an unverified institutional endorsement, BLOCKED until Partner gains a partner-side consent mechanism). For /places/list/: the view materialises up to 200 rows, so calling partner_for_place per row is a 200-query N+1 — extend the existing prefetch_related('partners') and pick the first verified+active partner in Python. DROP the reverse 'venues stewarded by this partner' section (Partner.place is a singular FK; partners.html already prints it). No model/migration.

**Depends on:** F37 Partner + Partner.objects.public() + partner_for_place; F16 JS-free /places/list/; F25 public_places() chokepoint
**Touches:** apps/web/views.py (extend prefetch; attach per-row + per-activity credit); apps/web/templates/web/_activity_card.html + activity_detail.html + places_list.html; apps/social/serializers.py (read-only host_venue_partner name); tests (credit drops on deactivate)

### F40 — Calm notification grouped view (anti-nudge by design)  `[M/imp2/med/revise]`
*Theme: Privacy and data-dignity as product*

**Pitch.** A render-time calm GROUPED VIEW of the in-app notification list (and a deferred quiet-hours batcher that ships only when web-push lands) — not a delivery-deferral that swallows mutable safety-relevant notices into a nightly digest.

**Why it fits the invariants.** Reinforces the no-dark-patterns / data-dignity promise without inventing tracking, PII, or any photo/feed surface; as a stated preference it is not behavioural inference. The DSA non-mutable carve-out (MODERATION/SYSTEM) and the CHILD guardian ARRIVAL real-time ping stay immediate. The grouped-view variant adds nothing to delivery timing, so no safety gate moves.

**Sketch.** Verification undercut the original 'digest' pitch: notifications are IN-APP ONLY (no push/email channel), so deferral reduces NO interruption — it only HIDES rows behind a summary, and ARRIVAL is a MUTABLE kind that fans out to a CHILD's guardians in real time (a naive 'defer all mutable' would batch a child-safety ping). SPLIT into two units. (1) NOW: group consecutive same-activity rows in the notification list at RENDER time — zero new delivery semantics, every notice still immediate, no swallow risk, delivers the 'calm'. (2) LATER: quiet-hours-aware batching that activates ONLY once Phase-2 web-push exists (the channel that actually interrupts), with the non-mutable + time-sensitive carve-outs (ARRIVAL, EVENT_REMINDER, JOIN_REQUESTED, MEETUP_CONFIRMED, CONNECTION_REQUEST, GROUP_QUESTION) pinned by tests. Do NOT ship the deferral-into-the-in-app-bell version.

**Depends on:** notify() single chokepoint + NON_MUTABLE_KINDS; NotificationPreference; Phase-2 web-push (the LATER half's only real value); apps/web notification list view (the grouped-view variant)
**Touches:** apps/web (notification list — render-time grouping); apps/notifications/models.py + services.py (LATER half only); apps/ops/run_due_jobs.py (LATER half only)

### F41 — 'Why this is recommended' feed-transparency panel (DSA Art.27)  `[S/imp2/low/revise]`
*Theme: Privacy and data-dignity as product*

**Pitch.** A no-JS 'How this feed works' tap-through on the home feed that honestly states the full ranking basis — your declared interests AND the activity types you've joined, cosine-similarity then soonest-first cold-start, request-only distance that's discarded — and links to the /my-privacy/ 'what we never collect' block.

**Why it fits the invariants.** Pure legibility on the app's strongest privacy story (recommendations from declared inputs, provably zero behavioural tracking). Read-only, self-scoped, cohort-neutral, no new data/signal/metric. A DSA Art.27 recommender-transparency posture. Derived entirely from the existing rec code path.

**Sketch.** Add recommendation_basis(user) returning the structured truth the code already computes. CRITICAL: user_vector folds in BOTH declared UserInterest types AND the types of activities the user has JOINED — the panel must disclose BOTH inputs, NOT 'declared only' or it is a false transparency claim (the codebase is already honest about this elsewhere). State that ordering is cosine-similarity, soonest-first on cold start, and that distance is request-only and discarded. Render in a no-JS <details> on home.html. LINK to /interests/ (the editable input) and to /my-privacy/ for the negative-space 'what we never collect' list — do NOT duplicate that block (it already exists). Add a guard test asserting user_vector's token sources stay interest+membership-only so the panel can't silently drift false.

**Touches:** apps/recommendations/services.py; apps/web/views.py (home feed); apps/web/templates/web/home.html; apps/recommendations/tests (input-source guard)

### F42 — Consent receipts folded into /my-privacy/  `[S/imp2/low/revise]`
*Theme: Privacy and data-dignity as product*

**Pitch.** Reframe the existing self-scoped audit-log + export trail of privacy choices (consent, age proof, mutes, blocks) as plain-language 'consent receipts' — folded INTO the shipped /my-privacy/ page rather than building a third ledger.

**Why it fits the invariants.** Pure data-dignity / GDPR-DSA legibility. Strictly self-scoped (actor_ref==user.id), field-allowlisted at the DB, read-only: it cannot create an adult-minor contact path, cannot widen PII, adds no public/photo surface, no tracking. Text-only and self-only by construction.

**Sketch.** The capability already ships three ways: audit_log_for (F34) already projects the SAME hash-chained AuditLog on actor_ref==user.id through _ACTIVITY_LOG_LABELS (which ALREADY contains the consent events — guardian link/accept, notification.preferences_updated, blocked/unblocked, media.*); my_privacy (F36) already renders live state; build_user_export already exports consents + guardianship timestamps for Art.20. So the only genuine gap is PRESENTATION framing. The right reshape: add a small 'Consent & permissions' filtered view of audit_log_for (a consent-only subset constant) AS A SECTION on the existing /my-privacy/ page, pairing each live-state row with its dated grant event — NOT a new /my-privacy/ sibling URL and NOT a parallel allowlist (which would drift from _ACTIVITY_LOG_LABELS). Touch build_user_export only if a labelled rollup is wanted on top of what it already emits.

**Depends on:** F34 audit_log_for / _ACTIVITY_LOG_LABELS; F36 my_privacy view + template; assurance_provenance / guardianship_capabilities / get_muted_kinds; build_user_export (already carries the timestamps)
**Touches:** apps/safety/services.py (consent-only subset constant + thin filtered helper); apps/web/views.py my_privacy (add section, no new URL); apps/web/templates/web/my_privacy.html; apps/accounts/export.py (optional labelled rollup)

### F43 — Connections-aware 'familiar faces are here' panel  `[M/imp2/med/revise]`
*Theme: Real-world and civic impact + sustainability*

**Pitch.** On a member-only activity detail, show the named familiar faces — accepted, mutually-consented connections who are current PEER members — so a newcomer sees a real person they already met is in the room. Adults-only, explicitly minor-suppressed, no count anywhere on a discovery card.

**Why it fits the invariants.** Surfaces only an EXISTING mutually-consented relationship (an accepted Connection = both sides opted in), behind the membership wall, never on a public/discovery surface. Connections are same-cohort only, and the panel is explicitly suppressed for CHILD/TEEN with a NEW cohort check. No stored rollup; eligibility derived live and block-rechecked both ways, mirroring group_roster's adult-member-only discipline.

**Sketch.** DROP the home-feed card COUNT entirely — 'N people you've met are going' is a raw cumulative count on a discovery surface (the exact pattern the recorded inv.2 lesson forbids) and a who-is-where oracle (probe activities to infer where a connection went — a soft presence signal). Keep ONLY the member-only activity_detail named panel: intersect connections_for(user) — ACCEPTED-only, block-filtered; NOT related_user_ids, which leaks PENDING/declined request state — with current_members (peers, exclude GUARDIAN role), re-check live MEMBER state + is_blocked both directions at render, member-gate the whole panel. Add a NEW explicit guard: show nothing unless viewer.cohort == Cohort.ADULT — the claimed _allowed_cohorts() gate DEFAULTS to all cohorts and would PASS minors; mirror group_roster's 'CHILD/TEEN -> None ALWAYS' rule explicitly.

**Depends on:** connections_for (ACCEPTED-only, block-filtered — NOT related_user_ids); current_members/voting_members (exclude GUARDIAN); a NEW explicit ADULT-only guard (NOT the connections enablement gate); blocked_user_ids/is_blocked (both directions at render)
**Touches:** apps/web/views.py activity_detail; templates/web/activity_detail.html; apps/connections/services.py; apps/social/services.py

## Stats

79 raw ideas → 47 after invariant filter (22 rejected) → 3 dropped in eval → 43 final across 8 themes.
