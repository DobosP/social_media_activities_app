# Status — social_media_activities_app

**Single source of current truth for this repo. New sessions start HERE** (not at
`docs/archive/COMPLETENESS_GAPS_2026-06.md`). On any doc conflict: this file > newest-dated ADR in
`docs/adr/` > everything else.

Last verified: 2026-07-04

## What this is

Activity-first, no-ads, deterministic/no-ML local-activities social app (children-first, in-person,
first launch city Cluj-Napoca; EU residency non-negotiable; donations only). `CLAUDE.md` has the
hard invariants (full conventions: `docs/ARCHITECTURE.md`; built-feature contracts:
`docs/FEATURES_BUILT.md`); `docs/SAFETY.md` is the safety-invariant authority.

## Current state

- **Mobile photo-heavy activity cards are accepted in this branch** (ADR-0007): one contextual
  cover photo per activity may appear on discovery cards, with generated accent fallback; no
  short video, galleries, public user photo feeds, like/pass/swipe telemetry, or engagement ranking.
- **The product engine (D1–D10 + four feature waves) is BUILT and tested** (~2150-green suite).
  Before building anything, read `docs/PRODUCTION_READINESS.md` **§0 "Already built — do NOT
  rebuild"** — a generic checklist wrongly flags features that already exist (Redis-ready caches/
  channels, opaque tokens, throttles, SSRF safe-fetch, GDPR erasure/export, pgvector ANN, prod
  boot assertions, CI gates, …).
- **API v1 hardening slice is implemented** (ADR-0008): canonical `/api/v1/` plus transitional
  `/api/` alias; DRF `URLPathVersioning`; bounded global limit/offset pagination; cursor/limit
  envelopes on v1 discovery, thread, messaging, social-list, and notification-style APIViews; and
  focused N+1/query-count guards for thread, notification, messaging, and social membership list
  surfaces.
- **DeferredTask has production task kinds registered** for bounded blob cleanup, activity
  notification fan-out, allowlisted cron-command splitting, and a fail-closed/audited media-scan
  placeholder. **Notification retention now schedules a bounded `notifications.retention_purge`
  task** that deletes only old read mutable notices; unread and MODERATION/SYSTEM safety/DSA notices
  are excluded. Media row-delete signals now enqueue blob cleanup instead of deleting storage on the
  request path; synchronous media scan admission remains fail-closed.
- **Database/read-path hardening slice is implemented** (ADR-0009 query/retention): Notification
  inbox reads have a concurrent `(recipient, -created_at)` index migration; `verify_audit_chain()`
  streams rows and exposes a verified high-water checkpoint helper for incremental extension checks.
  No migration linter dependency is present yet; zero-downtime CI linting remains open.
- **CSP enforcement hardening is implemented** (ADR-0010): executable inline scripts, inline event
  handlers, inline style attributes, and inline style blocks were removed from key CSP-smoked
  server-rendered pages; JSON/JSON-LD script islands carry CSP nonces; Leaflet/chat/offline-meetups
  flows use static JS; the shared policy no longer includes `style-src 'unsafe-inline'`; and
  `DJANGO_CSP_ENFORCE=True` remains the explicit enforcement switch after deployed violation reports
  are reviewed. Operators can group exported report-only payloads with `digest_csp_reports`.
- **Readiness and request-correlation observability are implemented** (ADR-0011): `/healthz` is
  cheap liveness only; `/readyz` checks the DB plus Redis cache and object storage only when those
  dependencies are configured; `X-Request-ID` is echoed, attached to log records, tagged in Sentry
  scope, and included in PII-safe structured request logs when enabled.
- **Open work** = the open **P0/P1/P2 items in `docs/archive/COMPLETENESS_GAPS_2026-06.md`** (gap tracker
  for the audited feature waves) + the remaining operational substrate in
  `docs/PRODUCTION_READINESS.md` (provisioning shared state, deploy-time Sentry/alert wiring,
  graceful shutdown readiness draining, edge security). Almost none of it is feature work.
- **Deploy**: launch target = **single Hetzner EU box + Hetzner Object Storage** via `deploy/`
  (Terraform + cloud-init) — see `docs/adr/0001` + `docs/HOSTING_EU.md`. `render.yaml` is a
  free-tier demo only. The Terraform has **never been applied — no infra exists**; never
  `terraform apply` (paid) without Paul. Launch itself is HARD-BLOCKED on the GDPR stack
  (DPIA + DPO + verifiable parental consent — org-level gate).
- **Cohort policy (code truth)**: all cohorts may use connections by default, **each strictly
  within its own cohort**; UNASSIGNED never; cross-age structurally impossible via the
  same-cohort gate — see `docs/adr/0002`. Groups self-creation still hard-walls CHILD/TEEN.
  Minor onboarding stays OFF in prod until a real trust anchor (EUDI wallet; age band, never DOB).

## Standard verification

```bash
docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest apps/ops/tests/test_deferred_tasks.py -q'
git diff --check
```

Full suite: `README.md` "Quick start" (local compose recipe); CI gates: `CLAUDE.md` ("Run & test").

## Agent notes

- Require human review for privacy, moderation, child-safety, or auth changes.
- Never read or print secret values.
- Git: commit locally on green; **do NOT push or merge unless Paul explicitly asks** (`AGENTS.md`).
- Docs: STATUS.md + ADR update is part of definition of done (see `AGENTS.md`).
