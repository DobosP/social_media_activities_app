# Release readiness — the "safe enough to launch" gate (D9)

Maps the launch gate in [SAFETY](SAFETY.md) (and the brief) to where each control is
implemented and verified. Status reflects code on `main`.

## Launch-blocking safety criteria

| Gate criterion | Where it lives | Status |
|---|---|---|
| Cohort isolation across discovery & threads | `apps/social/services.py` (`visible_activities`, `can_join`); pinned `Activity.cohort` | ✅ enforced + tested |
| Cohort isolation in chat | `apps/chat` consumer/service access checks (membership + cohort) | ✅ enforced + tested |
| Under-16 cannot participate without valid parental consent | `apps/accounts/services.can_participate`; gated in social/media/chat | ✅ enforced + tested |
| Reporting → moderation → action loop with audit logs | `apps/safety` (reports, moderation queue, hash-chained `AuditLog`) | ✅ implemented + tested |
| Image scanning + EXIF/GPS stripping on every upload path | `apps/media` pipeline (validate → strip → scan → store) | ✅ implemented + tested |
| Text-first; only profile pic + private in-thread photos | `apps/social` (text posts), `apps/media` (the only image paths) | ✅ enforced |
| No public adult↔minor private contact; no global DMs | chat is per-activity only; no DM system exists | ✅ by design |
| Private by default (threads/photos visible to members) | membership-scoped queries; signed, expiring media URLs | ✅ enforced + tested |
| Consent-based joining (two-thirds vote) | `apps/social` join-by-vote (default 2/3) | ✅ implemented + tested |

## Operational readiness

| Item | Where | Status |
|---|---|---|
| Liveness/readiness probe | `GET /healthz` (`apps/ops`) | ✅ |
| Privacy-respecting (aggregate-only) observability | `GET /api/ops/stats` staff-only; no per-user analytics (IS-6) | ✅ |
| Donation funding (no ads, no tracking) | `apps/donations` (pluggable provider, no card data stored) | ✅ |
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

All **engineering** launch-gate controls are implemented, integrated, and covered by the
automated test suite. Remaining items are **deployment provisioning** and **legal/compliance
sign-off**, which require a human owner and production credentials before a public beta in
the first city.
