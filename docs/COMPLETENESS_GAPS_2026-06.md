# Completeness & gap tracker — last-2-sessions features (2026-06-21)

Source: a 10-agent completeness audit of the complex features merged via PRs #64–#71 (the
2026‑06‑19 "P1 production-readiness" wave and the 2026‑06‑20 "accountability + discoverability"
wave). Average completeness ≈ **87%**. Every feature is real, tested code — the missing portion is
overwhelmingly **complete-code-inert-by-design** (default-off flags / awaiting prod creds), plus the
discrete follow-ups below.

Per-feature scores: progression-avatar 93 · observability 92 · public-discovery 90 · SEO 90 ·
P1-hardening 88 · media-storage 88 · IaC 85 · DSA-sanctions 82 · provider-resilience 82 ·
EUDI-identity 78.

Legend: `[x]` done · `[ ]` open · **P0** ship-blocker/correctness · **P1** compliance/product call ·
**P2** hardening/quality · **CFG** activation config (not a code gap).

---

## P0 — correctness

- [x] **SEO landing pages were `Cache-Control: public` while rendering cookie-bearing content.** A
  shared cache/CDN (the docs put Cloudflare in front, `docs/PRODUCTION_READINESS.md:77,164`) could
  replay (a) an authenticated user's per-user nav (unread count, has-guardian, connections flag) and
  (b) **any** visitor's per-session CSRF `Set-Cookie` + form token from the base-layout language form
  to other visitors. Fixed in `fix/seo-cache-private-on-auth`: `apps/web/seo.py:cache_public` now
  treats a request-bearing (base-layout) page as **never `public`** — authenticated → `private,
  no-cache`, anonymous → `private, max-age` (+ `Vary: Cookie`); the three `things_to_do*` views pass
  `request`; robots/sitemap/llms (no base layout, no cookies) stay pure-`public`. Regression tests in
  `apps/web/tests/test_seo_round3.py` (landing private for both anon+auth; public endpoints set no
  `Set-Cookie`). _Adversarial-review caught the anonymous CSRF-cookie leak the first pass missed._
- [ ] **(Follow-up, P2) Restore shared-CDN caching of landing HTML if desired.** They're now
  `private` (browser-only) because the base layout mints a CSRF cookie. To make them safely
  `public`-shared again, render landing pages with a cookie-less layout (no `{% csrf_token %}` form)
  or set `CSRF_USE_SESSIONS=True` so anonymous visitors get no `csrftoken` cookie/token. Low value at
  launch scale (the sitemap/robots that actually drive crawling are still `public`).

## P1 — compliance / product decisions

- [x] **DSA Art.17 redress is unreachable.** DONE (`feat/dsa-art17-redress`): a pre-auth
  `/account/restricted/` surface lets a restricted user (is_active=False) prove credentials WITHOUT a
  session to read their allowlisted statement of reasons, and a `ModerationAppeal` model +
  `file_appeal`/`resolve_appeal` services give the contest path (also reachable from `/my-safety-record/`
  and a DRF `AppealView`; resolution via admin + DRF). No email (in-app-only invariant) — redress is
  the pre-auth surface. Adversarially reviewed; the review caught + fixed a real MED reactivation bug
  (`lift_expired_suspensions` now ignores appeal-overturned/lifted sanctions). Residual LOW follow-ups:
  - [ ] Overturning a lifetime BAN reactivates the account but does **not** release the
    `BannedIdentity` ledger row (no `release_identity_ban` service yet; inert unless
    `IDENTITY_UNIQUENESS_ENFORCED`). Add `release_identity_ban` + call it from `_reverse_action`.
  - [ ] Overturning a REMOVE on a Post the author **also** self-deleted (both use `is_hidden`, no
    provenance) would resurrect it; rare + recoverable (author can re-delete). Add a provenance field
    or skip un-hide when author-deleted.
  - [ ] F19 `safety_record_for` caps own-content id sets (`[:500]`/`[:1000]`), so a user with more
    can't *contest* a decision on content beyond the cap from that page (the pre-auth surface still
    covers account sanctions). Query `ModerationAction` against the GFK columns directly.
- [ ] **Public discovery defaults to opt-OUT** (`Activity.is_publicly_listed=True`); migration `0030`
  default=True back-fills *all existing adult* activities/groups to public on deploy. Get explicit
  DPO/legal sign-off vs privacy-by-default; decide whether adults should default opt-IN. Confirm the
  back-fill is the intended rollout. `apps/social/models.py`, `apps/social/migrations/0030`.
- [ ] **Anonymous public card exposes exact start time + named venue** of an adult meetup to the open
  internet — consider a coarser time/venue display. Copy/privacy review. `apps/web/templates/web/discover.html`.
- [ ] **No DRF endpoint for the public-listing opt-out toggle** — API-first organisers can't hide an
  activity/group from public discovery (web-only today). `apps/discovery/` or `apps/social/` viewset.
- [ ] **Progression "Level N of 5" copy is borderline gamification (inv. 2).** Product call: keep the
  self-only level, or revert to a plain meetup count. Keep `PROGRESSION_AVATAR_PUBLIC` off and assert
  it can never combine with minor cohorts. `apps/accounts/avatars.py`, `apps/recommendations/services.py`.

## P2 — hardening / quality

### EUDI identity binding
- [ ] Implement the durable per-user holder-id record so `EUDIWalletProvider._holder_id` returns a
  real value and `_enforce_subject` actually binds a credential subject to an account (today always
  `None` → per-account anti-transfer enforcement is inert; cross-registration uniqueness still works
  via the `holder_hash` unique constraint). `apps/accounts/identity/providers/eudi.py`.
- [ ] Implement `release_binding` (set `IdentityBinding.released_at`) or remove the dead recovery path
  — the documented voluntary fresh-start path does not exist today. `apps/accounts/services.py`.
- [ ] Require a dedicated `IDENTITY_BINDING_SECRET`; it silently defaults to `SECRET_KEY`, so rotating
  `SECRET_KEY` would break every `holder_hash` lookup. Add a prod guard. `config/settings/`.
- [ ] Register `IdentityBinding` / `BannedIdentity` in `apps/accounts/admin.py` + a moderator path to
  inspect/unban/release (no way to lift a wrongful lifetime ban today).
- [ ] Make web sandbox `verify_age` (`apps/web/views.py`) call `bind_identity` for surface symmetry,
  or document why it intentionally does not.
- [ ] Add a direct test for the ban-rejection branch inside `bind_identity` (BannedIdentity → 403).

### DSA sanctions
- [ ] `lift_expired_suspensions` saves per-row without locking → two concurrent `run_due_jobs` could
  double-process. Add `select_for_update(skip_locked)` or a job lock. `apps/safety/...`.
- [ ] No HTTP-layer tests for the DRF moderation/referral views (permission gating, `suspend_days`→
  `expires_at`, TIMED_BAN duration validation, proof-view audit).
- [ ] Authority referral is a ledger only — no external transmission (INHOPE/IGPR). Document/
  operationalise the out-of-band reporting SLA, esp. CSAM mandatory-reporting timelines.
- [ ] Indefinite SUSPEND (no `suspend_days`) silently becomes permanent deactivation outside the BAN
  ledger — document to moderators + test.

### Media storage
- [ ] Built-in scanners are exact SHA-256 only (any re-encode/resize evades); perceptual dHash is a
  weak supplement. Decide on a managed perceptual (PhotoDNA-class) matcher for a child platform.
- [ ] Add tests for `docscan.py` (ClamdScanner INSTREAM + fail-closed; Noop) and `ManagedScanner`
  (happy + fail-closed-on-error); test the PDF fail-closed branch in `attach_to_post`.
- [ ] No automated orphan-blob reconciliation/observability (purge failures only logged).

### P1 hardening
- [ ] `render.yaml` `healthCheckPath` still points to `/healthz`, not the richer `/readyz` (which is
  shipped but unused by the reference deploy).
- [ ] `render.yaml` never provisions `METRICS_TOKEN` → `/metrics` 403s by default (no observability).
- [ ] "Covering indexes" is overstated — only one plain composite `AddIndexConcurrently`
  (`notifications/0017`); no INCLUDE/covering indexes, none added to `Post`/`AuditLog`.
- [ ] N+1 work was a single fix (`messaging.participant_keys`), not a systematic feed/thread/
  notification-list audit.
- [ ] Report-only CSP has no enforcement path wired (no nonce/inline-script extraction, no
  report-uri collector) — will sit report-only indefinitely without follow-up.

### Provider resilience
- [ ] Circuit-breaker mutators (`record_failure/record_success/allow`) aren't lock-protected (benign
  under CPython GIL, but a correctness smell). `apps/ops/resilience.py`.
- [ ] No half-open probe / success-threshold-to-close; recovery untested. No jitter/Retry-After, no
  per-call breaker metrics/alert, no admin reset tooling. No shared (Redis) breaker state across workers.

### Observability
- [ ] No CSP report-uri endpoint → report-only violations aren't collected (blocks moving CSP to enforce).
- [ ] `request_id` doesn't propagate into cron/job processes (defaults to `-` outside HTTP).
- [ ] No guard/test that `JsonFormatter` never serialises PII if a future log call passes a user object.
- [ ] django_prometheus counters are per-process — multi-worker deploy needs per-instance scrape/aggregation.

### SEO
- [ ] Fix stale docstrings/comments claiming a `301` redirect (`apps/web/seo.py:64`, `sitemaps.py`,
  the `test_seo_discoverability.py` comment) — actual behaviour is `200` + canonical link.
- [ ] Thin observability on the IndexNow job; consider surfacing submit success/failure to the heartbeat.
- [ ] No e2e test that a private/internal URL is rejected through `submit_urls` (relies on `safety/net.py`).

### IaC
- [ ] No remote/encrypted Terraform state backend — `*.tfstate` (DB password, S3 keys, Django secret,
  EUDI anchor) lives as local plaintext, only git-ignored. Add a `backend` block.
- [ ] Object storage is BYO and not provisioned by Terraform; the backup lifecycle/expiry rule is
  manual and `backup.sh` does no pruning → backups grow unbounded if the operator forgets.
- [ ] No IaC validation in CI (`terraform validate/fmt`, tflint, shellcheck, cloud-init lint).
- [ ] cloud-init has no retry/idempotency-on-failure; manual DNS/TLS bootstrap window; manual deploys
  (no green-CI-gated deploy hook). Single box is a deliberate launch-time SPOF.
- [ ] `server_type` default drift: `variables.tf` cpx21 vs README/example cpx22. Doc drift:
  `docs/HOSTING_EU.md` never names terraform/cloud-init/tfvars.

### Progression avatar
- [ ] Add an explicit test that a non-self user can never receive another user's progression via the
  web profile or `/me` path (today guaranteed by view structure, not asserted).

## CFG — activation config (not code gaps; "ships dark, flip when ready")

- [ ] **EUDI / identity uniqueness:** real `EUDI_TRUSTED_ISSUERS` (EU trust list) + `EUDI_CLIENT_ID`;
  flip `IDENTITY_UNIQUENESS_ENFORCED=True` once real wallets present key-binding proofs (~Dec 2026).
  Confirm it's ON in prod or a banned wallet can re-register.
- [ ] **Donations/booking:** wire Stripe (`STRIPE_SECRET_KEY` + switch `DONATIONS_PROVIDER` off the
  default deeplink) before the resilience breaker exercises anything live.
- [ ] **Media:** provision a lawful CSAM hash set (`MEDIA_CSAM_HASH_BLOCKLIST_FILE`) or a
  `ManagedScanner` endpoint — uploads fail **closed** in prod until then. Point `MEDIA_STORAGE_BACKEND`
  at S3 + supply EU bucket/creds/region; optionally enable `MEDIA_REDIRECT_TO_PRESIGNED`.
- [ ] **Observability:** `SENTRY_DSN`, `OPS_HEARTBEAT_URL`, `METRICS_TOKEN`, `LOG_FORMAT=json`.
- [ ] **SEO:** `SITE_BASE_URL` (custom domain); register/submit sitemap in Google/Bing consoles;
  `GOOGLE/BING_SITE_VERIFICATION`; optionally enable IndexNow with a generated key. Review hand-written
  `llms.txt` + Organization description copy.
- [ ] **Legal copy:** static legal pages are DRAFT placeholder text (`apps/web/views.py:3697`) — needs
  review/finalisation before launch.
