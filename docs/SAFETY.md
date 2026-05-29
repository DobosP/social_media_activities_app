# Safety by design

The product's core promise is a **safe place for children to meet peers and do real activities**.
Safety is not a feature bolted on at the end — it's a constraint on every deliverable. This doc is
the reference for those constraints. Primary delivery is **D4**, but the rules here bind D2, D3,
D5, and D6 too. See [ROADMAP](ROADMAP.md) and [COMPLIANCE](COMPLIANCE.md).

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
   (D6). No public photo feed, no public profiles beyond a minimal display name + avatar.
5. **Consent-based joining.** New members are admitted only via the **two-thirds vote** (D3), so a
   group controls who joins its activity.
6. **Text-first.** The only images are one profile picture and **private, in-thread** photos — both
   safety-screened (D6).

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
  visible; EXIF/GPS stripping; size limits; signed, expiring, membership-scoped URLs.
- **D10 (secure messaging):** username-addressable **direct & group** chat that is **end-to-end
  encrypted** (the server is a zero-knowledge relay storing ciphertext only). Because content
  scanning is impossible under E2EE, safety is enforced by **access control** — cohort isolation,
  invite-accept first contact, blocking, and rate limits — plus **report-with-decryption** (the
  reporter attaches the plaintext they can see), which feeds the D4 moderation loop. Honest
  cryptographic limits and the moderation trade-off are documented in [MESSAGING](MESSAGING.md).

## Offline-meetup safety (product/UX, later)

Because the whole point is meeting **in person**, add (in or after D3/D4): public-place defaults,
safety guidance prompts for minors, optional "bring a guardian" norms for younger cohorts, and
clear reporting from the activity screen. Track as safety backlog.

## Privacy stance (reinforces safety)

- **No behavioural tracking, no ads, no profiling** (also DSA Art. 28). Observability is aggregate
  only (IS-6).
- **Data minimization:** age bands over birthdates; store the least identity data that works.
- **Deletion & revocation:** parental consent can be revoked; accounts and their content can be
  deleted; define retention windows with the DPO.

## Definition of "safe enough to launch" (D9 gate)

- Cohort isolation verifiably enforced across discovery, threads, and chat (tests + review).
- Reporting → moderation → action loop works end to end, with audit logs.
- Under-16 cannot participate without a valid parental-consent record.
- Image scanning + metadata stripping active on every upload path.
- Security review / pen test passed; incident-response runbook exists.
