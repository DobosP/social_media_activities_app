# Status — social_media_activities_app

**Single source of current truth for this repo. New sessions start HERE** (not at
`docs/archive/COMPLETENESS_GAPS_2026-06.md`). On any doc conflict: this file > newest-dated ADR in
`docs/adr/` > everything else.

Last verified: 2026-07-07

## What this is

Activity-first, no-ads, deterministic/no-ML local-activities social app (children-first, in-person,
first launch city Cluj-Napoca; EU residency non-negotiable; donations only). `CLAUDE.md` has the
hard invariants (full conventions: `docs/ARCHITECTURE.md`; built-feature contracts:
`docs/FEATURES_BUILT.md`); `docs/SAFETY.md` is the safety-invariant authority.

## Current state

- **Merge-audit P1/P2 web fixes landed locally** (2026-07-07): React saved-search POSTs may submit
  activity type/category slugs and the server resolves them; home activity cards render contextual
  cover alt text under the enforced web contract; public-listing mutation input is fixed by
  ADR-0018 (`listed` canonical, legacy `is_publicly_listed` only when sent alone); Phase-3 React
  mutation POSTs have focused regression coverage.
- **Local RO-EDU seed is post-migrate data-only** (ADR-0017): `Dockerfile.db` installs pgvector
  only; compose runs `migrate` then `load_roedu_seed`, which loads `db/seed-data.sql` once and
  leaves schema/`django_migrations` solely to Django migrations.
- **Frontend redesign follow-ups (the open list, in rough priority order):**
  1. **Flip `SOCIAL_REACT_UI`** per environment once the React screens are reviewed
     on-device (dev already defaults ON; the kill switch stays for instant rollback).
  2. **Later migration program** (own sessions, own recon — deliberately NOT part of the
     shipped redesign): activity-detail React shell (embeds the live thread + F33
     pre-send safety nudge whose script order is load-bearing), E2EE messaging UI
     (crypto/IndexedDB/transport must stay byte-identical), Leaflet map screens,
     3d-force-graph, donation flows.
  3. **CSP unpkg removal slice**: Leaflet is vendored since Phase 1, so the
     `https://unpkg.com` allowances in script-src/style-src/img-src can go — needs the
     two tests that assert them updated in the same commit
     (apps/web/tests/test_csp_templates.py, apps/ops/tests/test_observability.py).
  4. **roedu-ui housekeeping**: `claude/csp-safe-styling` (v0.3.0, CSP-safe styling)
     merged to roedu-ui main; consider GitHub Packages publishing so consumers can drop
     the vendored-tarball pattern; promote genuinely shared screen components
     (TabStrip/EventCard patterns) upstream.
  5. **Legacy layer retirement** (after ALL screens migrate): delete the legacy
     templates the SPA replaced, collapse the token mirror (frontend/src/theme.ts
     becomes the single source; static/css/base.css keeps only server-chrome styles),
     remove the SOCIAL_REACT_UI branches from views.
  6. The campaigns progress meter keeps the app's one deliberate inline style (dynamic
     width); revisit if CSP style-src ever needs to cover it (e.g. width-bucket classes).
- **Redesign Phase 4 shipped** (`claude/redesign-social-p4`) — the redesign program's
  final phase: the sensitive subsystems were restyled IN PLACE, zero behavior changes.
  Messaging (e2ee-messaging.js untouched — it was already fully class-driven, so the P1
  token remap themes it), places map chrome, communities graph page (canvas got a real
  `.graph-canvas` class, deliberately dark in all themes for the vendored force-graph;
  graph JS untouched), donations/campaigns/transparency and safety/legal pages: all
  inline styles replaced with utilities (one deliberate exception: the campaigns
  progress-meter dynamic width). The React migration of these subsystems remains a
  SEPARATE future program per ADR-0016.
- **Redesign Phase 3 shipped** (`claude/redesign-social-p3`): the account & community
  surfaces — /you, /settings, profile, interests, topics, access, notifications (+
  preferences), connections, saved searches, communities list + community detail — are
  React screens behind the same `SOCIAL_REACT_UI` switch, all classic-POST round-trips
  (every P3 mutation redirects with a flash — no client mutation state). Account/inbox
  navigation now renders from ONE source (`account_nav`/`you_tabs`/`inbox_tabs` in
  views_spa.py), closing the recon's duplicated-nav finding. Child-safety pages (wards,
  guardianship, verify-age, privacy/safety/log, account delete) stay server-rendered and
  got a class-only restyle (wards nested cards subordinated; 20 inline styles removed
  with new u-*/fieldset-plain utilities). communities graph page untouched (vendored
  3d-force-graph); stale groups.html removed (route already redirected).
- **Redesign Phase 2 shipped** (`claude/redesign-social-p2`): home, activities browse
  (list + card deck), organizer console, **and the public SEO screens (events, places
  list, things-to-do index/city/detail)** rebuilt as React screens fed by per-view JSON
  bootstrap (`apps/web/views_spa.py` + `web/spa.html` + `?_data=1` soft navigation), behind
  the **`SOCIAL_REACT_UI` kill switch (default OFF — legacy SSR + full test suite unchanged;
  dev defaults ON)**. Public screens keep full SEO parity when the flag is on: same
  meta description/robots (noindex on filtered), JSON-LD, RSS alternates, breadcrumbs,
  plus a server-rendered crawler/noscript snapshot inside `#root` (web/snapshots/*) that
  React replaces on hydration; their `?_data=1` payloads carry no CSRF token so
  `cache_public` still applies. `/my-meetups/` intentionally stays SSR (F38 offline safety
  page). `activity_detail.html` split into 5 behavior-identical partials; its React shell
  is deferred to the sensitive track (embeds the thread + pre-send safety nudge).
  Remaining inline-style leftovers need a small new-utility set — folded into Phase 3.
- **Frontend redesign program is underway** (ADR-0016, branches `claude/redesign-social-p*`):
  React/Vite SPA with shared `@roedu/ui` (v0.3.0, CSP-safe) and the bespoke "Aurora Social"
  theme (indigo/teal, mobile-first, dark-native). Phase 1 shipped: token rebrand of the
  legacy CSS (light/dark/contrast), vendored Bricolage Grotesque display font, mobile
  bottom tab bar in base.html, `frontend/` scaffold + Docker node build stage + `spa_entry`
  nonce'd asset tag, `.btn--light` defined, Leaflet vendored locally (CSP unpkg allowance
  kept — removal is a follow-up hardening slice), inline-style cleanup batch 1. E2EE
  messaging/maps/graph/donations/safety UIs are untouched (restyle-in-place in Phase 4;
  migration is a later program). Theme values live in BOTH frontend/src/theme.ts and
  static/css/base.css until the legacy layer retires.
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
- **CSP enforcement hardening is implemented** (ADR-0014): executable inline scripts, inline event
  handlers, inline style attributes, and inline style blocks were removed from key CSP-smoked
  server-rendered pages; JSON/JSON-LD script islands carry CSP nonces; Leaflet/chat/offline-meetups
  flows use static JS; the shared policy no longer includes `style-src 'unsafe-inline'`; and
  `DJANGO_CSP_ENFORCE=True` remains the explicit enforcement switch after deployed violation reports
  are reviewed. The report-only collector at `/api/v1/ops/csp-report/` accepts unauthenticated
  browser reports with an 8 KiB body cap, stores only sanitized directive/blocked/document triples
  in process memory for tests/debugging, logs only those fields under a global budget, and operators
  can group exported report-only payloads with `digest_csp_reports`.
- **Explicit browser security headers are implemented** (ADR-0015): shared settings pin
  `nosniff`, `Referrer-Policy: same-origin`, `Cross-Origin-Opener-Policy: same-origin`, and a
  conservative `Permissions-Policy` that disables camera/microphone and scopes geolocation to self.
- **Readiness and request-correlation observability are implemented** (ADR-0011, ADR-0013):
  `/healthz` is cheap liveness only; `/readyz` checks the DB plus Redis cache and object storage
  only when those dependencies are configured; SIGTERM/SIGINT or the ops test seam flips `/readyz`
  to 503 with only a safe `draining` boolean while preserving liveness; `X-Request-ID` is echoed,
  attached to log records, tagged in Sentry scope, and included in PII-safe structured request logs
  when enabled.
- **Media egress presigned redirect is implemented** (ADR-0012): when
  `MEDIA_REDIRECT_TO_PRESIGNED=True` and the selected storage backend supports presigning,
  media-serving views re-check viewer authorization before returning a short-lived 307 object-store
  redirect. Local/dev/test filesystem storage still streams through Django; scanner and
  fail-closed upload gates are unchanged.
- **The child-safety anti-abuse limiter uses atomic cache primitives**: `allow_action()` seeds new
  fixed windows with cache `add()`/NX semantics and increments existing windows with backend
  `incr()`, preserving TTLs, limits, and the existing missing-key fallback behavior.
- **Open work** = the open **P0/P1/P2 items in `docs/archive/COMPLETENESS_GAPS_2026-06.md`** (gap tracker
  for the audited feature waves) + the remaining operational substrate in
  `docs/PRODUCTION_READINESS.md` (provisioning shared state, deploy-time Sentry/alert wiring,
  edge security). Almost none of it is feature work.
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
