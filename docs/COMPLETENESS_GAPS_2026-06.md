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

## ⏭️ NEXT SESSION — START HERE (updated 2026-06-21)

**Done & pushed this session** (origin/main @ `7663844`): P0 SEO cache-header fix · P1 DSA Art.17
redress (`[x]` below) · P1 privacy-by-default discovery (`[x]`) · P2 EUDI binding release paths +
admin tooling (`[x]`) · flaky chat/messaging consumer tests fixed (CI is green & deterministic again
— full suite **2083 passed, 0 errors** twice). Each was adversarially reviewed before merge.

**DONE → DSA sanctions hardening** (P2): row-locked `lift_expired_suspensions` (+ `_reverse_action`),
HTTP-layer moderation/referral tests, indefinite-SUSPEND + authority-referral-SLA docs. Merged to main
(`feat/dsa-sanctions-hardening`), adversarially reviewed (4 dimensions, 0 confirmed defects). See the
DSA-sanctions P2 block below (now all `[x]`).

**Recommended next pick → P2 batch (autonomous, pick one)**: circuit-breaker locking + half-open
(`apps/ops/resilience.py`) · `request_id` into cron jobs · CSP report-uri · covering indexes / N+1
sweep. _(DONE: `IDENTITY_BINDING_SECRET` prod guard + EUDI cluster — `feat/eudi-binding-hardening`;
media docscan/ManagedScanner/PDF fail-closed tests — `test/media-scanner-coverage`.)_

**Then, remaining open work** (all itemised below with file pointers):
- **P2 batch** (autonomous): `IDENTITY_BINDING_SECRET` prod guard · `request_id` into cron jobs ·
  circuit-breaker locking + half-open · media docscan/managed-scanner tests · render.yaml `/readyz` +
  `METRICS_TOKEN` (confirm Render vs Hetzner/Terraform deploy target first) · CSP report-uri ·
  covering indexes / N+1 sweep · SEO stale-301 docstrings · IaC remote state backend + CI validation.
- **P1 needing YOUR product/DPO call**: progression "Level N of 5" gamification copy (inv.2);
  coarser anonymous public-card time/venue display; DRF opt-in toggle for public listing (LOW).
- **DEFERRED (needs a protocol decision)**: EUDI strict per-account anti-transfer (`_enforce_subject`)
  — see the EUDI section for why.
- **CFG**: activation config before launch (EUDI trust list + flag, Stripe, media CSAM scanner + S3,
  Sentry/metrics/heartbeat, SEO domain/console tokens, legal-copy finalisation).

Cadence reminder: branch first → build → adversarial review (single agent for small, Workflow for
complex/safety) → `pytest -q` full suite + `ruff check`/`format --check` + `makemigrations --check` →
merge `--no-ff` → **ask before pushing** (the auto classifier blocks direct-to-main without per-instance OK).

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
  - [x] Overturning a lifetime BAN now releases the `BannedIdentity` ledger row
    (`feat/eudi-binding-followups`): `accounts.release_identity_ban` is wired into `_reverse_action`
    on BAN-overturn (no-op unless `IDENTITY_UNIQUENESS_ENFORCED` + wallet-bound).
  - [ ] Overturning a REMOVE on a Post the author **also** self-deleted (both use `is_hidden`, no
    provenance) would resurrect it; rare + recoverable (author can re-delete). Add a provenance field
    or skip un-hide when author-deleted.
  - [ ] F19 `safety_record_for` caps own-content id sets (`[:500]`/`[:1000]`), so a user with more
    can't *contest* a decision on content beyond the cap from that page (the pre-auth surface still
    covers account sanctions). Query `ModerationAction` against the GFK columns directly.
- [x] **Public discovery defaulted to opt-OUT.** DONE (`feat/privacy-default-discovery`, user-approved
  as the privacy-by-default reading of invariant #4): `Activity`/`Group.is_publicly_listed` now defaults
  **False**, both create paths no longer auto-list, and migration `0031` flips the default + un-lists
  every row `0030` had auto-listed (irreversible-noop reverse). `set_public_listing` (ADULT-only) is now
  the opt-IN toggle; the web toggle UI already renders the opt-in affordance. Adversarially reviewed:
  all three ADULT-only walls hold (no minor can ever be listed), no creation path missed, migration safe.
- [ ] **(LOW, optional) Anonymous public card exposes exact start time + named venue** of an adult
  meetup — consider a coarser display. Copy/privacy review. `apps/web/templates/web/discover.html`.
- [ ] **(LOW) No DRF endpoint for the public-listing opt-IN toggle** — API-first organisers can't make
  an activity/group discoverable (web-only). Add a cohort-gated `set_public_listing` action to the
  activity/group DRF viewset. `apps/discovery/` or `apps/social/` viewset.
- [ ] **Progression "Level N of 5" copy is borderline gamification (inv. 2).** Product call: keep the
  self-only level, or revert to a plain meetup count. Keep `PROGRESSION_AVATAR_PUBLIC` off and assert
  it can never combine with minor cohorts. `apps/accounts/avatars.py`, `apps/recommendations/services.py`.

## P2 — hardening / quality

### EUDI identity binding
- [ ] **DEFERRED (protocol uncertainty):** durable per-user holder-id so `_enforce_subject` enforces
  the credential subject per account (today `_holder_id` always `None`; cross-registration uniqueness
  already holds via `holder_hash`). Hinges on whether the EUDI credential `sub` is STABLE across
  credential renewal/key-rotation for our RP — strict rejection would falsely block a legitimate
  re-verify. Needs a protocol/product call before enabling; `release_binding` (now built) is the
  intended escape hatch once it lands. `apps/accounts/identity/providers/eudi.py`.
- [x] `release_binding` (set `IdentityBinding.released_at`) — built (`feat/eudi-binding-followups`):
  the documented voluntary fresh-start path now exists + is admin-invokable.
- [x] Require a dedicated `IDENTITY_BINDING_SECRET` — DONE (`feat/eudi-binding-hardening`): a prod boot
  guard (`config/settings/prod.py`) rejects a missing **or** SECRET_KEY-equal value when
  `IDENTITY_UNIQUENESS_ENFORCED` is on, so rotating `SECRET_KEY` can't silently rekey every
  `holder_hash` (identity uniqueness + the ban-evasion ledger). base.py default carries a pointer note.
- [x] Register `IdentityBinding` / `BannedIdentity` in `apps/accounts/admin.py` + a moderator path to
  inspect/release a binding and lift a wrongful identity ban — DONE (`feat/eudi-binding-followups`).
- [x] Make web `verify_age` call `bind_identity` for surface symmetry — DONE
  (`feat/eudi-binding-hardening`): a banned/already-bound wallet is now refused on the verify-age
  surface too (before assurance is applied), mirroring `register`/`EUDIVerifyView`; no-op in sandbox.
- [x] Direct test for the ban-rejection branch — DONE (`feat/eudi-binding-hardening`): a unit test of
  the `BannedIdentity` branch in `bind_identity`, an API-layer 403, and web verify_age refusal tests
  (banned + already-bound).

### DSA sanctions — DONE (`feat/dsa-sanctions-hardening`, merged to main; adversarially reviewed, 0 defects)
- [x] `lift_expired_suspensions` now processes each expiry in its own transaction with
  `select_for_update(skip_locked=True)` on the `ModerationAction` row (re-checked for `lifted_at`
  under the lock) + a blocking lock on the target account row, so two concurrent `run_due_jobs`
  ticks can't double-reactivate/audit/notify. The dignity notice fires strictly post-commit.
  `_reverse_action` takes the same account-row lock (lift-vs-appeal-reversal serialized).
- [x] HTTP-layer tests added (`apps/safety/tests/test_dsa_sanctions_hardening.py`): permission gating
  (resolve + referral POST + proof GET), `suspend_days`→`expires_at` (SUSPEND + TIMED_BAN), TIMED_BAN
  duration validation (400), proof-view audit, indefinite-SUSPEND-never-auto-lifts, multi-restriction
  lift-once dedup.
- [x] Authority referral out-of-band reporting SLA documented (`docs/RUNBOOK.md` Safety/incident-
  response): the ledger does NOT transmit; the on-call moderator owns the INHOPE/IGPR report within
  the legal window (CSAM = highest, without delay) and records the external reference. _External
  transmission integration (auto-forward to a hotline API) remains a future build, now operationalised
  as a manual duty._
- [x] Indefinite SUSPEND (no `suspend_days`) documented to moderators (RUNBOOK sanction-duration
  table: it never auto-lifts and is NOT on the `BannedIdentity` ledger — use `BAN` for permanent) +
  regression test.

### Media storage
- [ ] Built-in scanners are exact SHA-256 only (any re-encode/resize evades); perceptual dHash is a
  weak supplement. Decide on a managed perceptual (PhotoDNA-class) matcher for a child platform.
- [x] Add tests for `docscan.py` + `ManagedScanner` + the PDF fail-closed branch — DONE
  (`test/media-scanner-coverage`): `apps/media/tests/test_scanners.py` covers ClamdScanner
  (INSTREAM clean/infected/fail-closed-on-dead-daemon/framing), Noop + `get_document_scanner`, and
  ManagedScanner (clean/match/`flagged`/fail-closed on network+HTTP+malformed+unconfigured);
  `test_attachments.py` covers the `attach_to_post` PDF document-scan branch (fail-closed without a
  scanner, blocked-on-match, allowed-when-clean, image-skips-doc-gate, web post rollback). 37 new tests.
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
- [x] Circuit-breaker mutators now lock-protected — DONE (`feat/circuit-breaker-hardening`): a
  per-instance `threading.Lock` wraps `allow`/`record_success`/`record_failure` so a probe budget +
  failure count can't be raced. `apps/ops/resilience.py`.
- [x] Half-open probe + success-threshold-to-close + admin `reset()` — DONE
  (`feat/circuit-breaker-hardening`): CLOSED→OPEN→HALF_OPEN state machine; after the cooldown a
  single (configurable `half_open_max`) probe is admitted while the rest still fail fast,
  `success_threshold` consecutive probe successes close it, any probe failure re-opens with a fresh
  cooldown (no thundering herd). Recovery + lock-safety unit-tested.
- [ ] Remaining (lower priority): jitter / Retry-After honoring, per-call breaker metrics/alert,
  shared (Redis) breaker state across workers.

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
