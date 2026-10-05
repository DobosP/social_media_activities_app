# Release readiness — the "safe enough to launch" gate (D9)

Maps the launch gate in [SAFETY](SAFETY.md) (and the brief) to where each control is
implemented and verified. Status reflects code on `main`.

> **2026-05 audit correction.** This gate was reconciled against the code in
> [AUDIT_2026-05](archive/AUDIT_2026-05.md) (archived). Two prior claims here were **wrong**: a full **D10
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
| Media blobs in object storage (prod scale) | `apps/media/storage.S3StorageBackend` (S3-compatible; Hetzner Object Storage per [HOSTING_EU](HOSTING_EU.md)); set `MEDIA_STORAGE_BACKEND` + `MEDIA_S3_BUCKET` | ✅ available |
| Real-time chat served in prod (ASGI) | ~~`Dockerfile` runs `daphne config.asgi`~~ — superseded: the native Go server owns live transport ([ADR-0032](adr/0032-complete-native-go-backend.md)) | superseded |
| CI / landing gate | `.github/workflows` are dispatch-only ([ADR-0033](adr/0033-manual-github-actions.md)); landing needs local `scripts/check-native.sh` + `scripts/qualify-native.sh` (zero skips) and an independent reviewer ([ADR-0040](adr/0040-landing-and-deployment-review-gates.md)) | local gates; no automatic CI |
| Backups / restore, cost controls, CDN | see [RUNBOOK](RUNBOOK.md) | 📋 documented (provisioning is a deploy-time task) |

`apps/...` paths above are the Django-era implementation, now an offline oracle; the native equivalents live
under `services/server/internal/` ([ADR-0032](adr/0032-complete-native-go-backend.md)).

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

## Before first deployment (audit 2026-10-05)

Valid until: every item below is fixed or retired by ADR — then treat as history.
Not fixed yet; each blocks the first deployment of the Go runtime
([ADR-0040](adr/0040-landing-and-deployment-review-gates.md)). Landing on `main` does not clear them.

- [ ] GOV-4 — human review of auth/erasure/privacy/safety code: `services/authcore`, `internal/accounts`
      (login, erasure, bootstrap), `internal/safety`, media erasure, messaging authority, admin permissions,
      budgets; record reviewer name and reviewed revision. Done when that record names a revision at or
      before the deployed one and its findings are closed.
- [ ] GOV-1 / GO-RT-03 — `render.yaml` launches Go without `MEDIA_STORAGE_BACKEND`, `SITE_BASE_URL`,
      `DJANGO_ALLOWED_HOSTS`, `TRUSTED_PROXY_CIDRS`; web and `--due` cron exit at startup (storage check,
      `cmd/social-server/runtime.go`) and the blueprint auto-deploys on push. Keep-and-fix or retire Render.
      Done when `render.yaml` is removed by ADR, or sets every required setting with auto-deploy off and
      web and `--due` start against a synthetic configuration.
- [ ] GO-RT-01 — `internal/schema/baseline.sql` needs superuser/extension-owner rights (COMMENT ON EXTENSION
      postgis/vector; untrusted postgis_tiger_geocoder/postgis_topology): fresh `deploy/cloud-init.yaml.tftpl`
      bootstrap as the non-superuser `app` role fails. Done when `--migrate-only` succeeds as a NOSUPERUSER
      database-owner role with postgis/vector pre-created by a superuser, covered by a qualification test.
- [ ] GOV-3 / GO-RT-04 — `--migrate-only` (`internal/schema/migrate.go`) adopts any DB with `accounts_user`
      without checking `django_migrations`; legacy `django_session` rows are never deleted; no test adopts a
      Django-migrated database. Done when adoption refuses a DB without the expected `django_migrations`
      state, legacy sessions are deleted, and a qualification test adopts a Django-migrated fixture.
- [ ] GO-RT-08 — `Dockerfile` and `Dockerfile.db` base images float (no digest); apt packages are unpinned.
      Done when base images are digest-pinned and package versions pinned or snapshot-sourced.
- [ ] F1 / GO-01 — authentication hygiene still runs on the request goroutine. Before deployment,
      single-flight the sweep and move it off that goroutine; prevent old/new replica overlap, synchronize
      clocks and verify that the trusted proxy delivers the real client address (ADR-0039).
- [ ] GO-MEDIA-06 — 30 s server ReadTimeout (`cmd/social-server/main.go`) vs the 80 MB upload cap without a
      buffering proxy. Done when a test uploads the cap over a slow link within the configured
      timeouts, or the deployment template adds a buffering proxy with that limit.
- [ ] GO-RT-07 — readiness drain is marked immediately before `server.Shutdown` (`cmd/social-server/main.go`), so no
      probe sees `/readyz` 503 before connections close. Done when a pre-stop delay sized to the deployment's
      probe-driven balancer exists, with a test that `/readyz` reports 503 for that window.
- [ ] GOV-9 — no rollback runbook for the Go landing (revert range, Django fallback, DB restore drill).
      Done when [RUNBOOK](RUNBOOK.md) carries that procedure and the restore drill has been rehearsed once.

Paths under `internal/` and `cmd/` are in `services/server`.

## Verdict

The **core child-safety invariants** (cohort isolation, consent-gated participation incl.
messaging, guardian read-only, blocking, fail-closed media) are implemented, integrated,
and covered by regression tests. **However, the engineering gate is not yet fully met:**
the 2026-05 audit ([AUDIT_2026-05](archive/AUDIT_2026-05.md), archived) found launch-blockers that remain
open (Wave 1) — no shared cache so rate-limits/channel-layer are per-process; no
brute-force protection on login; retention/suspension purges are not scheduled in the
deploy; and no GDPR erasure path. Those plus **deployment provisioning** and
**legal/compliance sign-off** (a real CSAM scanner, DPIA, DSA Art. 28, EUDI prod
credentials, pen test) must be closed before a public beta in the first city.
