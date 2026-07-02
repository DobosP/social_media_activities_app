# ADR-0006: E2EE messaging — safety by access control + report-with-decryption, not content scanning

Date: 2026-05-29
Status: accepted

## Decision
Direct/group messaging (D10, `apps/messaging`) is **end-to-end encrypted** — the server is a
zero-knowledge relay (ciphertext + per-recipient wrapped keys) — and child safety is enforced by
**who can talk to whom**, not by reading content: cohort-isolated `can_message` (same cohort only,
never UNASSIGNED; the key registry itself 404s cross-cohort), invite-accept first contact,
blocking both ways, rate limits, metadata-only audit; **reporting works via the reporter's own
decrypted copy** (report-with-decryption). CHILD-cohort oversight = a guardian enrolled as an
additional **transparent, read-only, forward-only** recipient — never a server backdoor.
Decided 2026-05-29 (`48381c5`, D10); full reference `docs/MESSAGING.md`.

## Context / why
A children-first platform normally wants to scan DMs for grooming/CSAM; true E2EE makes that
impossible. The conflict is resolved deliberately in favour of E2EE + structural gates.
- **Why not plaintext + server scanning**: a plaintext store of children's private messages is a
  catastrophic breach target and a surveillance liability; the EU ePrivacy interim derogation for
  voluntary DM scanning expired 2026-04-03 and CSAR is unsettled — legal ground is unstable.
- **Why not client-side scanning**: rejected as a server concern; if CSAR ultimately mandates it,
  it belongs at the client (pre-encryption, e.g. on-device hashing) so the server stays
  zero-knowledge — the swappable scanning posture is that seam.
- **Why this is defensible**: an adult structurally cannot *address* a child (cohort gate before
  any crypto), no unsolicited contact, and no media can ride the E2EE channel (ADR-0004).

## Consequences
- The server can never mine/moderate DM content; moderation relies on participant reports
  (report-with-decryption) + metadata signals. This trade-off is documented, not hidden.
- Guardian oversight is consent-gated (ACTIVE `GuardianRelationship`), visible to all
  participants, read-only, and forward-only (no history back-fill).
- Group-thread media stays out of DMs entirely; anything scannable lives in cohort-gated threads.
- Revisit when the EU CSA Regulation lands — any scanning obligation needs a superseding ADR.
- Supersedes: the pre-D10 "no DM system exists" release-gate claim. Superseded-by: none.
