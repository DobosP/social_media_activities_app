# Threat model & security review (P7)

A STRIDE-style pass over the app's trust boundaries, the controls already in place,
and prioritized findings. Pairs with [SECURITY](SECURITY.md) (supply-chain + baseline),
[SAFETY](SAFETY.md) (minor protection), [COMPLIANCE](COMPLIANCE.md), and
[DATABASE](DATABASE.md). Gates the public beta (D9 definition of done).

## Assets (what we protect, worst-first)

1. **Minors' safety** — no adult↔minor private contact; no grooming surface.
2. **Children's personal data** — deliberately minimized: an **age band**, never a
   birthdate; usernames are parent-authorized identifiers, not PII.
3. **Private content** — thread posts, chat messages, thread photos.
4. **Accounts & sessions**; **moderation/audit records**; **donation records**.

## Trust boundaries

- **Anonymous → API**: public reads (places, events, taxonomy, donation total,
  `/healthz`, schema/docs) + donation start.
- **Authenticated user → cohort data**: activities, threads, chat, media — scoped to
  the user's age **cohort** and filtered by **blocks**.
- **Staff/moderator → admin + moderation/ops** endpoints.
- **External systems**: payment provider webhook, iCal/Google/Overpass ingestion,
  S3-compatible object storage. All untrusted input.

## STRIDE by surface (control inventory ✅ + gaps ▶️)

**Spoofing**
- ✅ Session/Token auth; `IsAuthenticated`/`IsAdminUser` on protected endpoints.
- ✅ Media access via **HMAC-signed, expiring URLs**, re-checked against thread
  membership on serve.
- ▶️ **Donation webhook** authenticates via a shared-secret header but compares with
  `!=` — use `hmac.compare_digest` to avoid a timing oracle. *(small code fix)*

**Tampering**
- ✅ All DB access through the ORM (parameterized); no string-built SQL.
- ✅ Signed media URLs prevent object-key guessing/tampering.
- ✅ **Hash-chained audit log** (D4) detects moderation-record tampering.

**Repudiation**
- ✅ Moderation actions + safety events recorded in the tamper-evident audit log.
- ▶️ Consider append-only retention/export of the audit log for incident forensics.

**Information disclosure**
- ✅ **Cohort isolation** enforced across discovery, threads, chat (children only see
  same-cohort peers/activities).
- ✅ **Blocking** filters discovery and user-generated content.
- ✅ **EXIF/GPS stripped** from uploads; image bytes never carry location.
- ✅ **Data minimization** — age band not DOB; **no card data** stored (opaque
  provider reference only); ops stats are **aggregate-only**, no PII.
- ▶️ Decide whether `/api/schema/` + `/api/docs/` should be **auth-gated in
  production** (they expose the full API surface).

**Denial of service**
- ✅ **DRF throttling** (anon + user) across the whole API.
- ▶️ Add **per-scope throttles** on the most abusable surfaces: auth/login, donation
  start, chat send, media upload, reporting (tighter than the global default).
- ▶️ DB **`statement_timeout`** + connection pooling (see DATABASE.md) bound runaway
  queries and connection exhaustion.
- ▶️ For multi-process deploys, back throttling with a **shared cache (Redis)** so
  limits are global, not per-worker.

**Elevation of privilege**
- ✅ Per-view permissions; cohort/membership checks in services, not just views.
- ▶️ **Least-privilege DB role** (app role ≠ superuser; separate DDL/read-only roles).
- ▶️ Enforce object-level checks on every write path in tests (see below).

## Minor-safety threats (the core promise — see SAFETY)

- **Grooming / adult→minor contact** → cohort isolation everywhere + guardian role is
  supervisory/group-only; no private 1:1 across cohorts. ✅ enforced; ▶️ add explicit
  regression tests asserting no cross-cohort DM/chat path exists.
- **CSAM upload** → pre-visibility image scanning seam (hash blocklist; swappable for a
  real matcher where lawful). ✅ seam; lawful matcher is a launch-gate decision.
- **Chat scanning/E2EE (CSAR)** → posture kept swappable; don't hard-code until the
  regulation finalizes. ✅

## Prioritized findings

| # | Finding | Severity | Fix |
|---|---------|----------|-----|
| 1 | Webhook secret compared with `!=` (timing) | Low | `hmac.compare_digest` |
| 2 | No least-privilege DB role / TLS / `statement_timeout` | Med | DATABASE.md §1,§6 |
| 3 | Global throttle only; no per-scope limits on auth/write | Med | `ScopedRateThrottle` per sensitive endpoint |
| 4 | Throttle counts per-worker (LocMemCache) | Med (at scale) | Redis cache for throttling |
| 5 | Schema/docs publicly expose full API surface | Low | optionally auth-gate in prod |
| 6 | No automated secret scanning in CI | Low | add gitleaks/trufflehog step (IS-2) |

## Pen-test / review checklist (OWASP API Top 10)

- [ ] **BOLA** — every object endpoint enforces ownership/membership/cohort (add
      `assertNumQueries`-style + 403/404 tests per write path).
- [ ] **Broken auth** — token/session expiry, no auth bypass on protected views.
- [ ] **Property-level authz** — `read_only_fields` prevent mass-assignment (✅ in
      serializers; verify per app).
- [ ] **Unrestricted resource use** — throttles + upload size caps + `statement_timeout`.
- [ ] **SSRF** — ingestion fetches only configured provider hosts; validate URLs.
- [ ] **Security misconfig** — `manage.py check --deploy` clean (✅ post-HSTS-preload).
- [ ] **Vulnerable deps** — `pip-audit` clean (✅); keep the bump workflow.
- [ ] **Insufficient logging** — audit log covers moderation/safety events (✅).

## Status

Document the residual risk acceptance with the DPO before the public beta. Findings
1–4 should land before launch; 5–6 are hardening.
