# Safety by design

The product's core promise is a **safe place for children to meet peers and do real activities**.
Safety is not a feature bolted on at the end — it's a constraint on every deliverable. This doc is
the reference for those constraints. Primary delivery is **D4**, but the rules here bind D2, D3,
D5, and D6 too. See [ROADMAP](ROADMAP.md) and [COMPLIANCE](COMPLIANCE.md).

> **Standing release posture:** the 2026-05-29 stress audit's **NO-GO for a public beta with
> minors** ([archive/AUDIT_STRESS_2026-05-29.md](archive/AUDIT_STRESS_2026-05-29.md) §1) remains
> the policy baseline — minors stay structurally OFF until DPIA + DPO + verifiable parental
> consent + a real EUDI trust anchor exist, regardless of engineering progress since
> ([PRODUCTION_READINESS](PRODUCTION_READINESS.md) §2e).

## Threat model (who we protect against)

- **Grooming / predatory adults** targeting minors — the top priority.
- **Peer harm** — bullying, harassment, doxxing.
- **Unsafe meetups** — bad actors using activities to reach children offline.
- **Illegal content** — especially CSAM in photos/chat.
- **Account abuse** — fake ages, takeovers, spam, ban-evasion.

## Core rules (invariants)

1. **Age-cohort isolation.** Children interact only within a **similar-age cohort**. Discovery,
   activities, threads, and chat are all cohort-scoped. Derived from the D2 age band — *exact birthdate
   is not needed and is avoided*.
2. **No private adult↔minor contact.** No cross-cohort DMs or private chat between adults and
   minors. Username-addressable direct & group messaging (D10) is **cohort-isolated** — you can
   only message users in your *own* age cohort — so an adult can never reach a child. First contact
   also requires the recipient to **accept** (no unsolicited messaging), and blocking is honoured
   both ways. Per-activity chat (D5) remains membership- + cohort-scoped. See [MESSAGING](MESSAGING.md).
3. **Verified age + parental consent before participation** for under-16 (D2). No consent → no
   access (age-gating).
4. **Private by default.** Threads and their photos are visible only to that activity's members
   (D6). Discovery cards may show one contextual cover photo only when the activity itself is
   visible; anonymous public cover cards remain adult-only through `public_activities()`. No public
   user photo feeds or public profiles beyond a minimal display name + avatar.
   Person visibility is TIERED (ADR-0028, `connections/profiles.py` is the sole resolver):
   vetoes first and 404-indistinguishable (blocked either way, cross-cohort, unassigned,
   inactive); a same-cohort stranger gets exactly the minimal cap above (display name +
   generated avatar); a live shared context (peer co-membership of an activity/group, or a
   pending join request with its organizer) adds the username handle, an age-verified boolean,
   and the shared context itself; a mutual connection adds messaging and — adults only —
   declared interests + the uploaded photo (profile page only, `can_view_photo` re-checked).
   Minor-cohort pairs stay clamped at the shared shape. Never shown at any tier: age band,
   cohort, progression, counts, attendance, history, last-seen.
5. **Consent-based joining.** New members are admitted only via the **two-thirds vote** (D3), so a
   group controls who joins its activity.
6. **Scoped media surfaces.** Images are limited to one profile picture, private in-thread photos,
   and one contextual activity cover photo on discovery cards — all safety-screened (D6). Video
   (ADR-0026) exists ONLY as a member's own-post attachment in a private, cohort-gated
   activity/group thread — adults-only at launch, withheld until its fail-closed processing
   succeeds, rendered solely inside the owning thread, never on discovery/public/feed
   surfaces, never in DMs, no autoplay/loops or engagement mechanics. Kill switch:
   `MEDIA_VIDEO_ENABLED=false`.

## Controls by deliverable

- **D2 (identity):** age-band assurance via EU mechanisms; verifiable parental consent + records;
  cohort assignment; minimal identity-data retention.
- **D3 (social):** cohort-scoped visibility/joining; join-by-vote; user-place quorum; conservative
  defaults (private, opt-in).
- **D4 (safety & moderation):**
  - **Reporting & blocking** on users, activities, posts.
  - **Moderation review queue** (built on Django admin) with actions (warn, remove, suspend, ban)
    and reason codes.
  - **Audit logging** of safety-relevant events; tamper-evident where feasible.
  - **Rate limiting / anti-abuse**; ban-evasion signals; new-account friction.
  - **Escalation path** for serious cases (incl. legal reporting where required).
- **D5 (chat):** rooms scoped to membership + cohort; moderation hooks; retention policy; scanning
  posture swappable pending CSAR ([COMPLIANCE](COMPLIANCE.md)).
- **D6 (media):** **image safety scanning** (CSAM hash-matching where lawful) before a photo is
  visible; EXIF/GPS stripping; size limits; signed, expiring URLs. Thread media stays
  membership-scoped; activity cover photos follow the owning activity's visibility gates
  (`visible_activities()` for authenticated viewers, `public_activities()` for anonymous adult-only
  public cards).
  Since W8 the built-in blocklist also matches a **perceptual (dHash) layer** (a casual
  re-encode/resize no longer evades it; honest limits in `apps/media/perceptual.py`), and
  PDFs pass a swappable **document/AV scanner seam** (clamd; fail-closed when required).
  Video attachments (ADR-0026, adults-only) extend the same posture: the
  ORIGINAL bytes' sha256 is screened fail-closed at upload, the clip is **withheld** until an
  off-request transcode strips all metadata (full re-encode) and **sampled frames pass the
  perceptual blocklist**; a frame match blocks the clip permanently and retains the source for
  moderation at the storage level (never servable in-app; staff see an explicit blocked
  placeholder). Minor-cohort video stays structurally off pending a lawful video-CSAM
  matcher decision (e.g. CSAI Match).
  The external-scanner integration plan (Arachnid Shield, PhotoDNA Cloud, NCMEC/esc_ABUZ
  reporting) lives in [MEDIA_FILTERING](MEDIA_FILTERING.md).
- **D10 (secure messaging):** username-addressable **direct & group** chat that is **end-to-end
  encrypted** (the server is a zero-knowledge relay storing ciphertext only). Because content
  scanning is impossible under E2EE, safety is enforced by **access control** — cohort isolation,
  invite-accept first contact, blocking, and rate limits — plus **report-with-decryption** (the
  reporter attaches the plaintext they can see), which feeds the D4 moderation loop. **Guardian
  oversight** for the under-16 cohort is the one sanctioned cross-cohort presence: a verified
  guardian can join a ward's conversation as a **transparent, read-only** observer (visible to all,
  cannot send, consent-gated). Optional **disappearing messages** minimize ciphertext at rest, and
  **key verification** (safety numbers) lets users detect a server MITM. Honest cryptographic limits
  and the moderation trade-off are documented in [MESSAGING](MESSAGING.md).

## Offline-meetup safety (product/UX, later)

Because the whole point is meeting **in person**, add (in or after D3/D4): public-place defaults,
safety guidance prompts for minors, optional "bring a guardian" norms for younger cohorts, and
clear reporting from the activity screen. Track as safety backlog.

- **Public-place gate for children (F9).** A CHILD-cohort meetup may only be set at a known public
  venue type — a staff-curated `places.ChildVenueClass` allowlist (library/park/school/sports/
  community), matched at read time, or a per-place staff approval (`ApprovedChildVenue`). Enforced
  in `create_activity`/`create_series`/`can_join`, fail-closed, behind `CHILD_PUBLIC_VENUES_ONLY`.
- **Venue moves are gated like creation (ADR-0019 §4).** An activity's place is still NOT in
  `ACTIVITY_EDITABLE_FIELDS`; the ONLY path that changes it is the audited `move_activity`
  service, which re-runs the same venue gates as creation — the `public_places()` chokepoint,
  the F9 child-safe-venue gate, and the ADULT-only own-pending-proposal carve-out — then
  notifies every member and supersedes stale reminders. So a move can never reach a venue that
  creation would have refused (no bait-and-switch into an ungated place). The one-shot plan-B
  fallback affordance was retired in favour of this path (owner decision 2026-07-07); the
  guardian-manifest "getting home" row retired with its field in the same decision.
- **Verified-adult supervisor seat (F29).** A CHILD activity may REQUIRE supervision
  (`Activity.supervised`, set at create or via the guarded `set_activity_supervision` — never via
  the editable-fields path). A join then cannot **settle** (`_admit`) until the owner's OWN verified
  guardian is seated as a **read-only** `GUARDIAN` member. The presence test is **live** (derived
  from current memberships, never stored) so the chip can't lie after a guardian leaves. The only
  adult who can enter stays keyed on an **ACTIVE `GuardianRelationship` to the OWNER** —
  `add_guardian` is **NOT** loosened to "guardian of any participant" (that would open an
  adult → other-people's-minors read-window). GUARDIANs remain excluded from posting, voting,
  reactions and the mention roster.

## Privacy stance (reinforces safety)

- **No behavioural tracking, no ads, no profiling** (also DSA Art. 28). Observability is aggregate
  only (IS-6).
- **Data minimization:** age bands over birthdates; store the least identity data that works.
- **Deletion & revocation:** parental consent can be revoked; accounts and their content can be
  deleted; define retention windows with the DPO.
- **Generated "constellation" avatar — declared-interest disclosure (deliberate).** The default
  generated avatar (when a user has not uploaded a photo) is a star-map of the user's *declared
  interests*: one colour-coded star per interest, coloured by its taxonomy category. Because the
  category→colour palette is fixed, a same-cohort viewer can infer a user's interest **count** and
  **category mix** from the picture. This is an accepted trade-off: interests are low-sensitivity,
  self-declared data; the avatar shows **no readable activity labels** (abstract nodes only); it is
  shown **only where any avatar is — same-cohort** (cohort isolation and the no-adult↔minor wall are
  untouched, since an avatar is not a shared activity); and nothing is stored (the image is derived
  on the fly, like the identicon it supersedes). It is **not** behavioural data — it reflects only
  what the user chose to declare, never inferred activity/attendance. Decided 2026-06-09; the avatar
  is the user's identity surface and is intentionally consistent across web and the messaging API.

## Definition of "safe enough to launch" (D9 gate)

- Cohort isolation verifiably enforced across discovery, threads, and chat (tests + review).
- Reporting → moderation → action loop works end to end, with audit logs.
- Under-16 cannot participate without a valid parental-consent record.
- Image scanning + metadata stripping active on every upload path.
- Security review / pen test passed; incident-response runbook exists.
