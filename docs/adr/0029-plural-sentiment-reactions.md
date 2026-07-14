# ADR-0029: Plural sentiment reactions without counts — appreciation, dissent, and conduct-concern channels

- Status: accepted (owner decisions 2026-07-14)
- Date: 2026-07-14
- Owner decisions baked in: adult public dissent line ON (hardened latching); TEEN footer kept,
  appreciation-only; moderation capacity kept with `automated` / `automated+human` modes and a
  moderation interface; launch-scale quietness of automated paths accepted.

## Context

`PostReaction` shipped in `9b5701e` as an anonymous, countless emoji-ack on thread posts
(distinct-emoji-set read surface; who+what reactions exist only client-side inside E2EE DMs).
The owner then asked for a richer, still-ethical surface: show an **overall feeling** on a post
— never a count, never who reacted, not even to the author — that is informative for personal
development, safe for children and adults, and cannot be engagement-farmed. Two follow-up
requirements: (1) a **corrective/negative** signal ("post is inappropriate / behavior not
supported / opinion discordance") that cannot become a bullying vector; (2) **plural coexisting
opinions** per post, kept deterministic and simple.

Two adversarially-reviewed design rounds (research + independent designs + red-team verdicts,
including hostile-clique simulations) back this decision. Full dossier:
`~/work/_temp/feat__reactions-v2-feeling/{gathered,designs,research2,designs2}.md`.

Key research anchors: demetrication evidence that **author-side count visibility is the
farming driver** (Instagram hid counts from viewers only; Grosser); anonymity thresholds in
360-feedback/course-eval practice (k=3–5 floors; tiny cohorts suppressed, not "fixed");
differential privacy unusable at n<20; Muchnik/Aral/Taylor (Science 2013) — visible tallies
herd; ICO AADC Standard 13 + EU DSA Art-28 guidelines name likes/peer-comparison as harmful
nudges for minors; the anonymous-negativity disasters (Yik Yak, Sarahah, ASKfm) share the
recipe *anonymous + negative + individually targeted + instantly delivered + unmediated*; no
literature system safely automates peer criticism to a minor (human mediation always);
restorative practice (Braithwaite): disapprove the act, accept the person.

## Decision — a severity ladder, each rung with its own disclosure geometry

"Negative" is unbundled. Public display is reserved for signals that target **ideas**; conduct
signals are never public; harm keeps its own channel.

| Rung | Signal | Targets | Disclosure |
|---|---|---|---|
| 0 | Appreciation (5 fixed facets) | effect-on-me | public plural sentences, thresholded |
| 1 | "I see this differently" | the idea | push-to-reply (speech); optional adult-only public line |
| 2 | "This doesn't seem to fit here" | conduct/placement | never public; capped private restorative note (adults) / human moderator (minors) |
| 3 | Report | harm/illegality | existing moderation path — the sole DSA Art-16 channel |

### Rung 0 — appreciation facets and the plural footer

- Vocabulary (fixed, positive-effect-only, operator-overridable set like today):
  `helped_me` 🙏 "Helped me" · `felt_welcome` 🤝 "Made me feel welcome" · `made_me_smile` 🙂
  "Made me smile" · `want_to_come` ✨ "Makes me want to come" · `got_me_thinking` 💡
  "Got me thinking". Existing emoji rows are data-migrated (👍/🙏→helped_me, ❤️→felt_welcome,
  🎉/👏→made_me_smile) as best-effort seeds.
- A facet **latches one public sentence** ("People found this helpful." / "People felt welcome
  here." / "This made people smile." / "This makes people want to come." / "This got people
  thinking.") only when **≥ k distinct reactors** chose it AND the **eligible audience ≥ 2k**
  (ADULT k=5, TEEN k=8; CHILD: no footer, ever). Eligible audience = live-computed thread
  members minus guardians minus blocked — never stored.
- Recompute by **daily batch** (DUE_JOB) — no live flips; defeats timing/diffing/toggle-probe
  attacks. Latched sentences are **re-derived from surviving rows** each batch (so GDPR erasure
  cascades honestly; disappearance at a daily boundary is non-attributable).
- Footer grammar: at most **two appreciation lines + one dissent line**, fixed catalog order,
  dissent always last, one sentence per line, no connectives, no icons-as-tally, never
  popularity-sorted, no "+more". Below threshold: **silence** (no "0", no "be the first").
- **Author parity**: the author's render is byte-identical to a viewer's. No per-reaction
  notification, ever (`notify()` is not called by any reaction write).
- The per-post live distinct-emoji-set display and its live broadcast are **removed** (they
  fired at n=1 — a small-roster identity leak). A reactor still sees their own toggle state.

### Rung 1 — dissent ("I see this differently")

- Lives in a low-prominence **Respond** menu (with rung 2 and Report), not inline: friction is
  protective. No emoji glyph (single glyphs read as mocking); text label only.
- The sheet's **primary action is "Add your view"** → a normal attributed thread reply
  (dissent-as-speech; works at any group size). Secondary, quieter: "Just note that I see it
  differently" → one anonymous tally row (toggle, withdrawable). CHILD cohort: secondary
  removed — a child can reply or back out, never silently disapprove a peer.
- **Adult-only public line** "Some see this differently." latches only when: ≥ 6 distinct
  dissenters AND eligible audience ≥ 12 AND the threshold held for **2 consecutive weekly
  recompute windows** (a one-day blitz latches nothing). It **lapses** after 2 consecutive
  windows below threshold (no permanent mark). `is_announcement` posts are **exempt** (never
  carry a dissent line). TEEN/CHILD posts never render it; teen tallies feed only the sensors.
- Dissent produces **no author-directed effect** at any k — no note, no ping.

### Rung 2 — conduct concern ("This doesn't seem to fit here")

- Pre-flag friction interstitial (Nextdoor-style) routing misuse away: *"'Doesn't fit here' is
  for a post that seems off-topic or out of step with this group — not for an opinion you
  disagree with (use 'I see this differently') and not for anything harmful or unsafe (use
  Report). No names and no numbers are ever attached, and a person only hears about it if a few
  others feel the same."*
- **Never public, any cohort, ever.** No free text (free text belongs to Report only).
- ADULT author: at **k1 = 2 distinct flaggers AND eligible audience ≥ 8**, the daily batch
  sends **one** private restorative note via `notify()` (new **muteable** kind
  `formative_note`): *"A few members felt one of your recent posts didn't quite fit the spirit
  of this group. No one has reported anything, nothing is hidden, and no names or numbers are
  attached — this is just a friendly heads-up so you can take another look if you'd like. If
  you edit the post, this note won't come back for it. You're a valued part of this group; this
  is about one post, not about you."* Caps: ≤1 note per author per rolling 14 days across all
  posts; ≤1 per post lifetime; editing clears the post's accrual and permanently bars a repeat
  auto-note (a re-cross after edit goes to the moderator queue). At **k2 = 4 distinct** →
  `ConcernReview` moderator-queue item (soft/formative — explicitly not an allegation).
- TEEN author: **never auto-delivered**; k = 3 distinct → moderator queue with a suggested
  human-relayed template. CHILD cohort: the concern affordance is **absent entirely** (no child
  flagger UI; children keep appreciation + Report). Guardians remain barred from all
  reacting/flagging (read-only supervisors).

### Sensor inversion (the anti-bully keystone)

Raw dissent/concern rows are moderator-only and audited. Daily detectors convert coordination
into detection instead of delivery:
- **Coordinated-flagging**: an overlapping flagger set hitting the same author across ≥3 posts
  in 14 days → moderator alert about the **flaggers**.
- **Pile-on protection**: one author drawing concerns across many posts in a short window →
  suppress further nudges, flag the **target for protective review**.
- **Flagger down-weighting**: a flagger firing on many distinct targets gets discounted.
These are trust-and-safety measures (documented as such — a lawful-basis/DPIA note, not "no
profiling"); they are moderator-facing only, 90-day-windowed, purpose-limited, and are **not**
a per-user reliability history (invariant 2): nothing is user-visible, nothing ranks users,
rows anonymize at 90 days.

### Moderation modes and interface (owner decision 4)

- New operator setting `MODERATION_MODE ∈ {"automated", "automated+human"}` (default
  `automated+human`).
- **Hard floor in BOTH modes** (not configurable): no automated delivery of any corrective
  signal to a minor; no automated content restriction anywhere in the soft channels (only a
  human acting through the existing Report/moderation tooling can hide/remove). In `automated`
  mode, minor-related and k2 items accumulate in the queue and **fail safe** (nothing is
  delivered or restricted); the interface shows the unattended backlog prominently.
- **Moderation interface** (`/moderation/`, moderator-gated, server-rendered): unified queue of
  `ConcernReview` items + sensor alerts, with actions — mark reviewed, relay the teen
  restorative template (human-authored send), escalate into the existing Report tooling
  (which carries DSA statement-of-reasons + contest rights), dismiss. Every action calls
  `record_audit` inside its transaction. The interface links, not duplicates, the existing
  Report queue. Fine-lines for when a human is *required* are configuration
  (thresholds/cohort routing above) so the owner can tune them without code changes.

### GDPR / DSA positions

- Rows are minimal: `(post, user, facet/type, created_at)`; no free text, no device/IP/location.
- Lawful basis: legitimate interest (community feedback + minor safety), heightened Recital-38
  safeguards; a DPIA note covers minors and the sensors.
- Retention: a new DUE_JOB anonymizes reactor/dissent/concern user FKs after 90 days (build —
  no such job exists today). `on_delete=CASCADE` handles account deletion; aggregates re-derive
  from surviving rows at the next batch.
- **SAR posture**: dissent/concern rows are the **flagger's** personal data (a record of their
  action), not the author's; an author SAR ("all flags about me") yields neither counts nor
  identities — Art-15(4) rights-of-others redaction (standard 360-feedback posture). Guardian-
  mediated SARs for minors follow the same rule.
- **DSA**: soft signals allege no illegality, trigger no automatic restriction, and emit no
  statement of reasons — they are not Art-16 notices. Report remains the sole, clearly-labelled
  Art-16 entry point (distinct styling, below a divider in the Respond menu). Art-25/28:
  no counts, streaks, badges, pings, or peer comparisons; conservative minor defaults.
- No new public/API exposure: the footer renders server-side inside the already-authorized
  thread read; dissent/concern rows are never serialized; the agent-access surface (ADR-0025)
  continues to exclude `social.*` — regression-tested.

## Alternatives rejected

- **Per-reaction percentages**: counts in disguise; small-n identity leak (33% of 3 = 1 person);
  recreates an optimizable metric; implies false precision.
- **Single "overall liked/disliked" verdict**: a valence axis creates a dislike-bomb vector and
  a grade; a single axis is a total order (rankable). Plural incomparable facets form a partial
  order — no leaderboard is constructible.
- **Author-visible counts/dashboards**: the demetrication evidence says author-side visibility
  is the load-bearing farming driver.
- **Live updates**: enable timing/diffing/toggle-probe deanonymization and refresh-compulsion.
- **A public "doesn't fit" mark**: public shaming (stigmatizing, not reintegrative), and
  weaponizable absence/presence.
- **Automated peer criticism to minors**: no safe precedent in the literature; contradicts
  AADC/Art-28 conservative defaults.

## Consequences

- Small groups (below floors) show no aggregate — accepted (course-eval/Officevibe precedent):
  anonymity below those floors is mathematically fake. Push-to-reply and moderator judgment
  are the small-group mechanisms.
- The praise+dissent pair is a deliberate, owner-approved relaxation of the "no valence pair"
  reading of invariant 1 — bounded by: idea-framed wording, adult-only, thresholded + sustained
  windows + decay, announcement exemption, no counts, dissent always last.
- A determined adult dyad can trigger at most one gentle note per 14 days on a rival — bounded
  residual, visible to the clique sensor on repeats.
- Moderation value depends on the queue being worked; unattended it fails safe but detects
  nothing. Owner will fine-tune human-required lines via configuration.
- `docs/FEATURES_BUILT.md` "reactions: explicitly OUT" line is stale versus `9b5701e` and is
  corrected as part of this change; SAFETY.md gains the minor-protection rules above.

## Implementation phases

1. Models + migrations: facet slugs on `PostReaction` (+ data migration), `PostDissent`,
   `PostConcern`, `PostSentimentFooter` (denormalized, re-derived), `ConcernReview`.
2. Services: shared thread-write gate; `toggle_reaction` (audited, no footer broadcast),
   `toggle_dissent`, `record_concern`; `sentiment_footer_for(post, viewer)` (cohort-stripping,
   render-time `is_hidden` guard); facet catalog + sentence map.
3. Batch jobs (DUE_JOBS): `recompute_post_sentiment` (daily; weekly dissent windows),
   `evaluate_concerns` (ladder + caps), sensors, `anonymize_stale_reaction_rows` (90d).
4. Web UI: footer partial, picker update, Respond menu (dissent sheet, concern interstitial,
   Report link), notification kind rendering.
5. Moderation interface + `MODERATION_MODE`.
6. Tests: gate parity, threshold/floor/window edges, author-parity, cohort stripping,
   caps/free-pass, sensors, no-count/no-who regression across web + DRF + agent surfaces.
7. Docs: this ADR, SAFETY.md, FEATURES_BUILT.md, STATUS.md.

## Implementation deltas

- **Group-thread UI deferred → now CLOSED (see delta (g)).** The service layer
  (`_thread_write_gate`, `toggle_reaction`, `toggle_dissent`, `record_concern`,
  `eligible_audience_count`) always handled both `Activity` and `Group` owner objects, but at first
  no Group-thread web surface called them for posts (`group_detail.html` rendered a hand-rolled
  post loop, not the shared partial). Round 3 (owner decision 2026-07-15) wires the full surface to
  group threads — see delta (g). The ladder is now live on both ACTIVITY and GROUP threads.
- **E2EE-DM reaction picker labels.** `apps/messaging`'s client-side, who+what DM reaction
  picker (explicitly out of this ADR's scope — it never touches `PostReaction`) reads
  `social.allowed_reactions()` purely for its button set. Since that function now returns facet
  slugs instead of emoji glyphs, the DM picker's rendered labels changed from glyphs to slug
  text — a cosmetic regression on a separate system, not a safety or invariant change. Left
  unfixed pending a DM-specific constant, owner's call on follow-up.
- **Live-arrived posts + the Respond menu.** A post delivered over the live socket renders the
  Respond menu (dissent/concern/Report) only after a reload — a documented limitation, not a
  gate hole (the write services re-gate every action regardless of how the affordance was drawn).
- **Eligible audience excludes blocked-vs-owner.** `eligible_audience_count` now also subtracts
  any member in a block against the thread owner (both directions, matching `is_blocked` /
  `_thread_write_gate`) — the anonymity denominator counts only members who could actually react,
  so it matches the "minus blocked" wording. It is memoized per owner within a batch run.
- **Muting is an honored opt-out; caps are mute-independent.** If an author has muted
  `formative_note`, the note is not delivered — but the attempt is still consumed
  (`PostConcernState.note_sent_at` is stamped), so the ≤1-per-author (rolling) and
  ≤1-per-post-lifetime caps hold regardless of the mute (the cross-post cap reads
  `PostConcernState`, not the Notification table). The muted attempt is audited as
  `concern.formative_note_muted`. A teen human relay to a muted member surfaces an honest "the
  member has muted these notes — nothing was delivered" message to the moderator, still marks the
  item reviewed, and audits `concern.note_muted`.
- **Escalate is restricted.** Only `CONCERN_ESCALATED` and `TEEN_CONCERN` items may be escalated
  into the Report tooling; SENSOR_* items are informational (moderator-facing) and the escalate
  affordance is hidden for them (server-side backstop rejects it too). Escalation targets the POST
  only — never `subject_user` — so a `SENSOR_PILEON` item can never file a Report against the
  protected victim. Actions are idempotent: only an OPEN item transitions, so a double escalate
  files a single Report. Opening a review that renders a post body records a `concern.viewed`
  audit row.
- **Coordinated sensor watches BOTH channels.** The coordinated-flagging detector intentionally
  aggregates both dissent AND concern rows (dissent-brigading detection per the red-team design);
  it is moderator-facing only, never author-directed.
- **Hidden posts leave the ladder.** A moderator REMOVE sets `is_hidden` without bumping
  `updated_at`, so the concern ladder and the footer recompute now explicitly skip hidden posts
  (they no longer accrue/latch/deliver). The anti-bully SENSORS still count rows on hidden posts —
  bullying a hidden post is still bullying — because they read the raw rows directly.
- **Graduation survives the 90-day purge.** A facet latched continuously past
  `REACTION_ROW_RETENTION_DAYS` graduates to `appreciation_permanent` even when its supporting
  rows fall below `k` (they have aged into the purge window; the sentence is now a non-personal
  aggregate). An erasure BEFORE 90 days still unlatches honestly.
- **(f) Friction rebalance (owner decision 2026-07-15).** The Respond menu was too effortful ("I
  see this differently" and the concern flow each cost multiple opens/taps). Owner directive: UI
  friction was NEVER the primary protection — the thresholds, audience floors, daily batching,
  caps, and the coordination sensors are (all unchanged and remain the primary safeguard). Friction
  drops to **one open + one tap**: the flattened menu (`_post.html`) shows three rows immediately on
  open — a dissent row (primary "Reply with your view" + a one-tap quiet "I see this differently"
  tally, with the reply nudge moved to AFTER the act instead of gating it), a one-tap conduct-concern
  toggle, and the Report link below a divider. No nested `<details>`, no sheets. The concern
  educational interstitial is now **paid ONCE per device** (localStorage key `concernIntroSeen`, set
  on first concern toggle OR first Respond-menu expansion) — there is **no server-side per-user flag
  and no new personal data**. Education **persists for no-JS users** (the intro `<div>` renders
  server-side and is only hidden by JS — progressive enhancement). The viewer's own toggle state is
  echoed server-side on a no-JS reload via `social.dissent_concern_mine` (the viewer's OWN rows
  only — never another member's, never a count), so "Noted quietly — tap to withdraw" is honest
  without JS. All systemic protections, thresholds, and the CHILD/TEEN cohort walls are unchanged.
- **(g) Group threads carry the full surface (round 3).** Group threads are the **primary home** of
  the feature per the owner. `group_detail` now renders posts through the SAME `_post.html` partial
  as activities (single source of truth for the cohort-gated markup) with the full context contract:
  the appreciation picker, the countless `p.sentiment_lines` footer, `p.reaction_mine`,
  `p.dissent_mine`/`p.concern_mine`, and `show_dissent_concern = (viewer.cohort != CHILD and
  group.cohort != CHILD)`. Five endpoints mirror the activity ones under the same shared write gate
  and JSON/redirect duality — `group_post_react` / `group_post_dissent` / `group_post_concern` /
  `group_post_edit` / `group_post_delete` at `groups/<pk>/posts/<post_id>/<action>/`. The partial was
  **generalized, not forked**: it reverses its per-post URLs from context-provided URL-name vars
  (`post_react_url_name` … `post_delete_url_name`) + `post_owner_pk`, so activities and groups share
  ONE partial. No service/model/job/migration change was needed (the services were already owner-
  generic; `GroupMembership` has no GUARDIAN role, so guardian exclusion is a safe Activity-only
  no-op; `eligible_audience_count` and the batch jobs already handle Group owners). The daily batch
  latches the countless footer for TEEN/ADULT group threads; the CHILD-group wall holds end-to-end
  (no footer, no dissent/concern UI, and the endpoints reject a CHILD flagger). The group thread is
  also now **live** (the config-driven `thread-chat.js` + F33 presend nudge, with group URL
  templates), matching the activity thread.
