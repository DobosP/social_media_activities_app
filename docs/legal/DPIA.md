# Data Protection Impact Assessment (DPIA)

> **STATUS: DRAFT — scaffolded from the verified code state for a qualified DPO/lawyer to
> finalize. NOT legal advice.** A DPIA is **mandatory** here (GDPR Art. 35): large-scale
> processing of **children's** personal data, age verification, and innovative tech (E2EE).
> Authoritative technical state: `docs/AUDIT_STRESS_2026-05-29.md`.

## 0. Document control
| | |
|---|---|
| Controller | _<legal entity — TBD>_ |
| DPO | _<name + ANSPDCP registration — TBD>_ |
| Date / version | _<TBD>_ / 0.1-draft |
| Sign-off | _<DPO + management — pending>_ |

## 1. Description of processing
- **Purpose:** connect people to organize **in-person** activities at real places; text-first.
- **Data subjects:** adults and **minors** (cohorts CHILD <16, TEEN 16–17, ADULT).
- **Personal data (data-minimized — `apps/accounts/models.py`):** username (parent-authorized
  identifier, not necessarily real-name/email), display name, **age band — never a birthdate**,
  derived cohort, identity-verification status/timestamp, declared interests, activity/membership
  data, text posts, optional profile + private thread photos, E2EE message ciphertext (server is a
  zero-knowledge relay — no plaintext), donation records (no card data — Stripe-hosted), a
  hash-chained safety audit log.
- **Special categories:** none deliberately collected; free-text may incidentally contain them →
  mitigated by E2EE (messages), moderation, and minimization.
- **Recipients / processors:** hosting (Render, EU/Frankfurt), object storage (S3/R2, EU),
  payments (Stripe), age-verification issuer(s) (EUDI), error tracking (Sentry, opt-in). DPAs
  required — see `ROPA.md`.
- **Retention:** governed by `*_RETENTION_DAYS` + the `run_due_jobs` scheduler. **DPO must set the
  periods** (default 0 = disabled).

## 2. Necessity & proportionality
- Lawful basis per purpose (**DPO to confirm**): legitimate interest / contract for the core
  service; **GDPR Art. 8 parental consent** for under-16s (RO digital-consent age = 16).
- Minimization: age band not birthdate; cohort isolation; **no behavioural ad profiling**; recs use
  only declared interests + joins (verified — DSA Art. 28 conformant).

## 3. Risks & implemented mitigations (cross-ref the audit)
| Risk | Mitigation (implemented) | Residual |
|---|---|---|
| Adult↔minor contact / grooming | Cohort isolation across discovery/threads/chat/messaging; consent gate; guardian observers read-only; per-delivery WebSocket re-auth | **Parental consent not yet cryptographically verifiable → minor onboarding OFF in prod** (L-GUARDIAN) |
| Child sees content after removal/ban | Moderation REMOVE hides content; ban/cohort-change evict live sockets (`can_view`/`can_access_thread` fail closed) | — |
| CSAM | Fail-closed upload gate; hash-only scanner seam; uploads OFF until a lawful matcher | CSAM reporting SOP outstanding (L-CSAM-SOP) |
| Unlawful retention | Scheduler + retention settings | **Periods unset — DPO to define** |
| Right to erasure | `erase_user` deletes account + authored ciphertext + media blobs; audit anonymized | — |
| Data residency | Frankfurt region pin + EU-S3 boot assertion | DPAs / SCCs outstanding |
| DoS / abuse | Rate limits, body-size cap, NUM_PROXIES, SSRF guard, decompression-bomb guard | Per-process limits until Redis (scale) |

## 4. Residual risks & beta-blocking gates
Blocking for a minors beta: DPIA sign-off, DPO appointment, processor DPAs, finalized Privacy
Policy/Terms, RO-counsel confirmation of the parental-consent standard + L190/2025 status, and a
verifiable age/guardianship trust anchor. See `COMPLIANCE_CHECKLIST.md` + the audit P0 list.

## 5. Open questions for the DPO/lawyer
1. Lawful basis per purpose; Art. 8 consent mechanism accepted by ANSPDCP.
2. Retention periods per category (with Art. 17(3) audit/safety carve-outs).
3. International-transfer basis (Stripe US → SCCs).
4. DSA Art. 18 criminal-notification duty + enterprise-size classification.

*Items requiring counsel (uncertain): ePrivacy derogation status, CSAR trilogue, EUDI timeline, RO
L190/2025 in-force date — see `docs/AUDIT_STRESS_2026-05-29.md` §4.*
