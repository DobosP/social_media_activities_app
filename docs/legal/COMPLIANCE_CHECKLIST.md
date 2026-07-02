# Compliance Checklist — GDPR / DSA / CSAR / EUDI / Romania

> **STATUS: DRAFT — for a qualified DPO/lawyer to finalize. NOT legal advice.** Owner column:
> **E** = engineering (in-repo), **D** = DPO/legal (human artifact). **B** = blocks a public beta
> with minors. Reconciled with `docs/archive/AUDIT_STRESS_2026-05-29.md`.

## Phase 0 — Governance (D)
- [ ] **B** Appoint + register a DPO with ANSPDCP; publish contact (replace `dpo@example.org`).
- [ ] **B** Designate the controller legal entity; finalize binding **Privacy Policy + Terms**
  (`docs/legal/` + `apps/web` `/privacy` `/terms` are DRAFTs).
- [ ] Complete **ROPA** (`docs/legal/ROPA.md`) and **DPIA** (`docs/legal/DPIA.md`) sign-off — **B**.
- [ ] **B** Execute processor **DPAs** (Render, S3/R2, Stripe, EUDI, Sentry) + SCCs for Stripe (US).

## Phase 1 — Age assurance & parental consent (E+D)
- [x] (E) Minor onboarding **OFF by default in prod** (`ALLOW_MINOR_ONBOARDING`) until a real anchor.
- [ ] **B** (D + RO counsel) Confirm the **verifiable parental consent** standard (Law 190/2018
  "reasonable efforts") and **L190/2025** in-force status.
- [ ] **B** (E+D) Wire a real trust anchor (EUDI guardian flow / national eID / blessed claim-code)
  before re-enabling minor onboarding; EUDI holder-binding + non-empty `EUDI_TRUSTED_ISSUERS`.

## Phase 2 — Minor safety & DSA (E+D)
- [x] (E) Cohort isolation; consent gate; guardian observers read-only; per-delivery WS re-auth.
- [x] (E) DSA Art.16 reporter ack/outcome; Art.17 statement of reasons.
- [ ] (E) DSA Art.20 internal appeal endpoint (or remove the "you may contest" promise) — pre-launch.
- [ ] (D) DSA Art.28 evidence memo (conformance is real: no ads/profiling); size-class + Art.11/12
  SPOC + Art.18 applicability (counsel).

## Phase 3 — Content safety / CSAR (E+D)
- [x] (E) Fail-closed uploads; hash-only `ManagedScanner` seam; uploads OFF by default.
- [ ] **B** (D + mod lead) **CSAM reporting SOP** (IGPR + INHOPE/esc_ABUZ, chain-of-custody) — see
  `BREACH_RUNBOOK.md`.
- [ ] (D) Confirm: do **not** add E2EE content scanning (ePrivacy derogation lapse) — counsel.

## Phase 4 — Data-subject rights & retention (E+D)
- [x] (E) Art.17 erasure (account + authored ciphertext + media blobs); Art.20 export endpoint.
- [ ] **B** (E+D) Run the `run_due_jobs` scheduler (render.yaml cron) + **set retention periods** per
  category (`*_RETENTION_DAYS`; default 0 = disabled).

## Phase 5 — Security hardening (E)
- [x] HSTS/secure cookies; deny-by-default DRF perms; rate limits + body cap + NUM_PROXIES; SSRF
  guard; decompression-bomb guard; webhook auth; stored-XSS sanitize; erasure-resilient audit chain.
- [ ] (E) Independent security review / pen test — **B** before public beta.
- [ ] (E) Redis + `DJANGO_REQUIRE_SHARED_STATE` + multi-worker for scale (global rate limits / WS).

## Phase 6 — Residency & deploy (E+D)
- [x] (E) Frankfurt region pin; EU-S3 prod boot assertion.
- [ ] (E) Paid Postgres + automated backups + restore rehearsal; OPERATIONS.md.
- [ ] (D) NIS2 applicability memo (likely out-of-scope at beta).

## Open uncertainties (counsel must resolve)
ePrivacy derogation status; CSAR trilogue outcome; EUDI rollout timeline; RO L190/2025 in-force date;
DSA Art.18 + enterprise-size classification. See `docs/archive/AUDIT_STRESS_2026-05-29.md` §4.
