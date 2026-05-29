# Release readiness — the "safe enough to launch" gate (D9)

Maps the launch gate in [SAFETY](SAFETY.md) (and the brief) to where each control is
implemented and verified. Status reflects code on `main`.

> **2026-05 audit correction.** This gate was reconciled against the code in
> [AUDIT_2026-05](AUDIT_2026-05.md). Two prior claims here were **wrong**: a full **D10
> direct/group messaging** subsystem *does* exist (the old "no DM system exists" line is
> removed below), and image scanning was a **no-op** as shipped. Both are corrected, and
> the messaging consent/cohort gaps were fixed (Wave 0). Several launch-blockers remain
> open (Wave 1) — the engineering gate is **not** fully met yet; see the audit.

## Launch-blocking safety criteria

| Gate criterion | Where it lives | Status |
|---|---|---|
| Cohort isolation across discovery & threads | `apps/social/services.py` (`visible_activities`, `can_join`); pinned `Activity.cohort` | ✅ enforced + tested |
| Cohort isolation in chat | `apps/chat` consumer/service access checks (membership + cohort) | ✅ enforced + tested |
| Under-16 cannot participate without valid parental consent | `can_participate` gated in social, booking, chat, media **and messaging** (the 2026-05 audit closed the messaging gap); consent grant/revoke now self-service via `POST/DELETE /api/accounts/wards/<id>/consent/` | ✅ enforced + tested |
| Reporting → moderation → action loop with audit logs | `apps/safety` (reports, moderation queue, staff resolve API, hash-chained `AuditLog`) | ✅ implemented + tested |
| Blocking enforced in discovery (blocked pairs don't see each other) | `apps/safety.blocked_user_ids` → `apps/social.visible_activities` | ✅ enforced + tested |
| Temporary suspensions auto-expire | `apps/safety.lift_expired_suspensions` + `lift_suspensions` command | ✅ implemented + tested |
| Image scanning + EXIF/GPS stripping on every upload path | `apps/media` pipeline; **fails closed** — scans the *original* bytes and refuses uploads unless a real scanner / non-empty blocklist is configured (`MEDIA_REQUIRE_SCANNER`). NB: a real CSAM matcher is still a launch-gate config task | ✅ pipeline fixed + tested; ⏳ wire real scanner |
| Text-first; only profile pic + private in-thread photos | `apps/social` (text posts), `apps/media` (the only image paths) | ✅ enforced |
| No private adult↔minor contact | Per-activity chat is membership+cohort scoped. **D10 direct/group messaging exists** and is cohort-isolated, invite-accept, **consent-gated** (Wave 0), block-aware, with guardian observers read-only and pruned when the consent basis ends | ✅ enforced + tested |
| Private by default (threads/photos visible to members) | membership-scoped queries; signed, expiring media URLs | ✅ enforced + tested |
| Consent-based joining (two-thirds vote) | `apps/social` join-by-vote (default 2/3) | ✅ implemented + tested |

## Operational readiness

| Item | Where | Status |
|---|---|---|
| Liveness/readiness probe | `GET /healthz` (`apps/ops`) | ✅ |
| Privacy-respecting (aggregate-only) observability | `GET /api/ops/stats` staff-only; no per-user analytics (IS-6) | ✅ |
| Donation funding (no ads, no tracking) | `apps/donations` (pluggable provider, no card data stored; deep-link default + **Stripe Checkout** provider) | ✅ |
| Media blobs in object storage (prod scale) | `apps/media/storage.S3StorageBackend` (S3 / Cloudflare R2 / MinIO); set `MEDIA_STORAGE_BACKEND` + `MEDIA_S3_BUCKET` | ✅ available |
| Real-time chat served in prod (ASGI) | `Dockerfile` runs `daphne config.asgi` | ✅ |
| CI gate (ruff, format, migrations, tests, pip-audit, docker build) | `.github/workflows` | ✅ |
| Backups / restore, cost controls, CDN | see [RUNBOOK](RUNBOOK.md) | 📋 documented (provisioning is a deploy-time task) |

## Compliance / process (owner: project + DPO, pre-public-launch)

These are process artifacts to finalize with a human before onboarding real minors —
tracked here, not code:

- [ ] DPIA finalized; Privacy Policy + Terms published (IS-4).
- [ ] DSA Art. 28 / Romania Online-Age-of-Majority review signed off.
- [ ] Real identity/age-assurance provider configured (`IDENTITY_PROVIDER`) — the EUDI
      provider exists; production credentials/endpoints must be set.
- [ ] CSAM hash blocklist source wired (`MEDIA_CSAM_HASH_BLOCKLIST` / real scanner).
- [ ] Independent security review / pen test passed.
- [ ] Incident-response runbook rehearsed (see [RUNBOOK](RUNBOOK.md)).

## Verdict

The **core child-safety invariants** (cohort isolation, consent-gated participation incl.
messaging, guardian read-only, blocking, fail-closed media) are implemented, integrated,
and covered by regression tests. **However, the engineering gate is not yet fully met:**
the 2026-05 audit ([AUDIT_2026-05](AUDIT_2026-05.md)) found launch-blockers that remain
open (Wave 1) — no shared cache so rate-limits/channel-layer are per-process; no
brute-force protection on login; retention/suspension purges are not scheduled in the
deploy; and no GDPR erasure path. Those plus **deployment provisioning** and
**legal/compliance sign-off** (a real CSAM scanner, DPIA, DSA Art. 28, EUDI prod
credentials, pen test) must be closed before a public beta in the first city.
