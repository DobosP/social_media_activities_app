# Compliance (EU / Romania)

Engineering-facing summary of the legal landscape and how it maps to the build. This is the
"can we deploy this in Romania, and what does the law require?" answer.

> **Not legal advice.** Before onboarding real minors, engage **Romanian counsel** and appoint a
> **Data Protection Officer (DPO)**. Treat this as a checklist to brief them, and revisit it —
> several items below are *moving* through 2026.

## Verdict

**Yes, it's deployable in Romania** — and the timing is favourable, because the EU is shipping the
exact identity/age tooling this product needs in **2026**. But a *children + identity + chat* app
sits in the most heavily regulated corner of EU tech law, so the compliance work is real and must
precede launch (see [ROADMAP](ROADMAP.md) integrated step **IS-4**).

## The instruments that matter

### 1. Identity & age assurance — eIDAS 2.0 / EUDI Wallet (do NOT roll your own ID)

- Under **eIDAS 2.0**, every EU member state must offer an **EU Digital Identity (EUDI) Wallet**
  by **December 2026**. **Romania** is building its national wallet (partnership with Mastercard),
  targeting that deadline.
- The EU's **privacy-preserving age-verification app** is **feature-ready (Apr 2026)**: it proves
  "over 13 / 16 / 18" using zero-knowledge techniques **without revealing name or birthdate**, and
  is interoperable with the EUDI Wallet.
- **What this means for us.** Our "EU re-certification for all users" and "unique child identifier
  with parental permission" requirements should be implemented **on top of these EU mechanisms**,
  not a bespoke ID scheme. Store an **age band**, not a birthdate, wherever possible. This is **D2**
  via the `IdentityProvider` abstraction ([ARCHITECTURE](ARCHITECTURE.md)).
- On *"legal requirements for the government to allow people to publish their own IDs"*: you do not
  build or operate an ID registry. You **consume** state-issued/eIDAS attestations and keep only the
  minimal derived attribute (age band + consent status). That sidesteps most of the burden of
  handling identity documents yourself.

### 2. GDPR + Romania's data-protection law (children)

- **Romania's digital age of consent is 16** (Law 190/2018, implementing GDPR Art. 8). Processing
  the data of a child **under 16** is lawful only with **parental consent**, and the controller must
  make **reasonable efforts to verify** that consent.
- **Romania's "Online Age of Majority" law** (adopted by the Senate Oct 2025; progressing through
  the Chamber of Deputies) sets **16** as the online age of majority and imposes, for services used
  by minors: **verifiable parental consent**, **age-gating** (no under-16 without validated parental
  consent), content-moderation and advertising duties, a **180-day transition** for existing minor
  users, and **fines of roughly 0.1%–0.4% of national turnover**.
- **What this means for us.** D2's parental-consent flow must be **verifiable** and **recorded**
  (who consented, when, scope, revocation/expiry). Age-gating is mandatory. Track this bill to
  final text before launch.

### 3. Digital Services Act (DSA) — protection of minors

- **DSA Art. 28(1)** requires platforms accessible to minors to ensure a **high level of privacy,
  safety, and security**, and bars **profiling-based advertising to minors**.
- **What this means for us.** Aligns with our design anyway (no ads, no tracking, safety-by-design).
  Document the minor-safety measures (this doc + [SAFETY](SAFETY.md)) as our Art. 28 evidence.

### 4. Chat & the CSA Regulation ("Chat Control") — keep the chat design flexible

- The interim **voluntary-scanning** derogation **expired (Apr 2026)**. The proposed mandatory
  **CSAR** ("Chat Control 2.0") is **in trilogue with a deal targeted around mid-2026**; debate
  continues over mandatory scanning and end-to-end encryption.
- **What this means for us.** Build **D5 chat** with a **swappable** moderation/scanning/encryption
  posture so we can comply with whatever CSAR finalises **without re-architecting**. Don't hard-code
  an encryption stance yet.

### 5. DPIA — required, not optional

- A **Data Protection Impact Assessment** (GDPR Art. 35) is required for large-scale processing of
  children's data. It must be done **before** D2 goes live (IS-4).

## Compliance checklist (mapped to deliverables)

| Requirement | Where | Gate |
|---|---|---|
| EU data residency (EU hosting) | IS-3 | Before first real deploy |
| DPIA completed | IS-4 / D2 | Before real-minor onboarding |
| Privacy Policy + Terms + DPA with processors | IS-4 / D9 | Before real-minor onboarding |
| Age assurance via EUDI / EU age-verification | D2 | D2 |
| Verifiable parental consent for under-16 + records | D2 | D2 |
| Age-gating (no under-16 without consent) | D2 | D2 |
| No profiling-based ads to minors (DSA 28) | Product principle | Always |
| Reporting/blocking + moderation + audit logs | D4 | D4 |
| Cohort isolation (no adult↔minor private contact) | D2 + D4 | D4 |
| Swappable chat scanning/encryption posture (CSAR) | D5 | D5 |
| Image safety screening (CSAM hash-matching where lawful) | D6 | D6 |
| Security review / pen test before launch | D9 | Launch |
| Track "Online Age of Majority" final text | ongoing | Launch |

## Open questions for counsel / DPO

- Final form and in-force date of Romania's "Online Age of Majority" law, and exact
  parental-consent **verification** expectations.
- Final CSAR text and its implications for our chat (scanning obligations, E2EE).
- Whether/how to rely on national eID vs. the EUDI Wallet during the 2026 rollout window, and any
  interim parental-consent mechanism before wallets are widespread.
- Lawful basis and method for any image safety scanning (CSAM detection) under EU law at launch.
- Data-retention periods (messages, consent records, audit logs) and deletion guarantees.
