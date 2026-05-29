# Records of Processing Activities (ROPA) — GDPR Art. 30

> **STATUS: DRAFT — scaffolded from the verified code state for a qualified DPO/lawyer to
> finalize. NOT legal advice.**

## 0. Controller / DPO
| | |
|---|---|
| Controller | _<legal entity, address — TBD>_ |
| DPO | _<name, contact, ANSPDCP registration — TBD>_ |

## 1. Processing activities
| # | Activity | Data categories | Lawful basis (DPO to confirm) | Recipients/processors | Retention |
|---|---|---|---|---|---|
| P1 | Account & identity | username, display name, **age band (not birthdate)**, cohort, verification status | LI / contract; **Art.8 consent** (<16) | Render | account life + audit carve-out |
| P2 | Age assurance | proven age band, provider, expiry (no identity docs stored) | legal obligation / consent | EUDI issuer(s) | until re-verification |
| P3 | Parental consent / guardianship | guardian↔ward link, consent status | Art. 8 | Render | while active + proof retention |
| P4 | Activities & membership | activity, role, votes, posts (text) | LI / contract | Render | `*_RETENTION_DAYS` (DPO) |
| P5 | Per-activity chat | plaintext messages (cohort-scoped) | LI / contract | Render | `CHAT_RETENTION_DAYS` |
| P6 | E2EE direct/group messaging | **ciphertext only** + wrapped keys (zero-knowledge) | LI / contract | Render | `MESSAGING_RETENTION_DAYS` + disappearing timers |
| P7 | Media (profile + thread photos) | image blobs (EXIF/GPS stripped), hashes | LI / consent | S3/R2 (EU) | until deletion/erasure |
| P8 | Safety: reports/moderation/audit | reports, actions, hash-chained audit (pseudonymized actor_ref) | legal obligation / LI | Render | audit-retention basis |
| P9 | Donations | amount, currency, provider ref (**no card data**) | contract / LI | Stripe | accounting period |
| P10 | Observability (opt-in) | aggregate metrics; errors (PII-scrubbed) | LI | Sentry (if enabled) | short |

## 2. Processors & DPAs (status: **all TBD — DPO to execute**)
| Processor | Role | Location | DPA | Transfer basis |
|---|---|---|---|---|
| Render | Hosting (web, Postgres) | EU/Frankfurt (pinned) | ☐ | EU |
| AWS S3 / Cloudflare R2 | Media object storage | EU (boot-asserted) | ☐ | EU |
| Stripe | Payments (hosted checkout) | US/EU | ☐ | **SCCs (US)** |
| EUDI issuer(s) | Age attestation | EU | ☐ | EU |
| Sentry | Error tracking (opt-in) | _<region TBD>_ | ☐ | TBD |

## 3. International transfers
Posture: EU-resident by default (web+DB Frankfurt; S3 EU; prod boot-asserts EU S3). **Stripe (US)
needs SCCs**; non-Render prod must set ALLOWED_HOSTS/CSRF (audit CFG-4). DPO to document Ch. V basis.

## 4. Security measures (summary — see DPIA + audit)
Cohort isolation; consent gate; E2EE relay; fail-closed media scanning + EXIF strip; HSTS/secure
cookies; deny-by-default DRF perms; rate limits + body cap + SSRF guard; hash-chained tamper-evident
audit; GDPR-erasure-resilient audit chain (`actor_ref`).

*Sources/uncertainties: see `docs/AUDIT_STRESS_2026-05-29.md` §4.*
