> **SUPERSEDED (2026-07-02):** as the session entry point — new sessions start at the repo-root
> `STATUS.md`, never here. The open **P0/P1/P2 items below remain the live gap tracker** for the
> audited 2026-06 feature waves (STATUS.md points here for them).

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

## ⏭️ NEXT SESSION — START HERE (updated 2026-06-23)

> **Session entry moved (2026-07-02):** new sessions start at `STATUS.md`, not this file. This doc
> remains the gap tracker for the audited feature waves (open P0/P1/P2 items below).

**Done & pushed this session** (origin/main @ `6433978`, suite **2151 passed**):
- Topic-preference feed + SOFT stated topic steering; guardian controls CHILD ward's feed
- `?view=list|card` text-first browse modes + Cards carousel: pointer-drag, ←/→, reduced-motion
- Phone-like Cards: generated abstract SVG accents (`activity_accent_svg`, procedural, XSS-safe)
- Media docscan + ManagedScanner + PDF fail-closed test coverage (37 new tests)
- EUDI: `IDENTITY_BINDING_SECRET` prod guard + web `verify_age → bind_identity` symmetry + ban-rejection tests
- Circuit-breaker CLOSED→OPEN→HALF_OPEN + per-instance `threading.Lock` + admin `reset()`
- CSP report-uri collector at `/api/v1/ops/csp-report/` + `request_id` in cron + PII-guard test
- SEO: stale-301 docstring corrections + IndexNow SSRF e2e test

All adversarially reviewed before merge. Working tree clean, 0 ahead/behind origin/main.

**Remaining open work is itemised in the sections below. Build plans: `## NEXT STEPS` at the bottom.**

**Product/DPO calls still needed before touching**:
- Progression "Level N" gamification copy (inv.2) — keep or revert to plain count?
- Coarser anonymous public-card time/venue display — exact or rounded/neighbourhood?
- PhotoDNA / perceptual hash vendor — which managed scanner for a child platform?

**Cadence**: `git checkout -b feat/<name>` → build → adversarial review (single agent for small;
Workflow for complex/safety) → `pytest -q` + `ruff check`/`format --check` + `makemigrations --check`
→ local merge `--no-ff`. The cadence **ends there**: do NOT push/merge unless Paul explicitly
asks — see `AGENTS.md`.

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
- [~] Report-only CSP: the **report-uri collector is now wired** (`feat/observability-hardening`), so
  violations are collected — the remaining step to ENFORCE is nonce/inline-script extraction.

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
- [x] CSP report collector — DONE (`feat/observability-hardening`): `CSPReportView` at
  `/api/v1/ops/csp-report/` + `report-uri`/`report-to` (+ `Reporting-Endpoints` header) wired into the
  report-only policy. AllowAny/no-auth/never-throttled, always 204s, bounded body, logs only
  operational fields (control-char-stripped, no log-forging) behind a global per-minute log budget.
- [x] `request_id` into cron — DONE (`feat/observability-hardening`): `run_due_jobs` stamps a per-run
  `job:run_due_jobs:<hex>` id into the logging contextvar (no more `-` outside HTTP).
- [x] JsonFormatter PII guard — DONE (`feat/observability-hardening`): regression test pinning the
  allowlist formatter never serialises a user object attached to a record.
- [ ] django_prometheus counters are per-process — multi-worker deploy needs per-instance scrape/aggregation.

### SEO
- [x] Fix stale `301`-redirect docstrings/comments — DONE (`feat/seo-cleanup`): corrected `seo.py`
  `place_path`, `sitemaps.py`, `seo_tags.py`, and the `test_seo_discoverability.py` comment to the real
  behaviour (200 + canonical `<link>`, never a redirect). Docs/comments only — no behaviour change.
- [ ] Thin observability on the IndexNow job; consider surfacing submit success/failure to the heartbeat.
- [x] e2e test that a private/internal URL is rejected through the IndexNow submit — DONE
  (`feat/seo-cleanup`): `test_submit_urls_blocks_a_private_endpoint_before_any_network` points the
  endpoint at a link-local IP and asserts `safe_get` rejects it (UnsafeURLError) BEFORE any
  `requests.request` — no exfiltration to e.g. the cloud metadata service.

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

---

## NEXT STEPS — concrete plans for remaining open items

Ordered by size + dependency. Each item is ready to build unless marked "decision needed first."

### Small builds (< half-day each)

**1. render.yaml health + metrics wiring**
Two config-only lines in `render.yaml`.
- Change `healthCheckPath: /healthz` → `/healthz` is fine for liveness but `/readyz` is the richer
  shipped endpoint. Change to `healthCheckPath: /readyz` (checks DB + cache connectivity).
- Add `METRICS_TOKEN` to the env var list so `/metrics` returns 200 instead of 403.
No code change; no test needed; no review required.

**2. Progression cross-user visibility test**
`apps/web/tests/test_profile.py` (or wherever the public profile view is tested).
Add `test_profile_does_not_leak_progression_to_others`: log in as user B, GET `/profile/<user-A-pk>/`,
assert neither `progression_level` nor `intensity` appears in the response (base avatar only).
Verifies the "guaranteed by view structure" claim with an explicit regression guard.

**3. DRF opt-in toggle for public listing**
`apps/social/views.py` — add a `@action(detail=True, methods=["post"])` named `set_public_listing`
to `ActivityViewSet` (and `GroupViewSet`). Call `social.services.set_public_listing(activity, value)`.
Gate: `request.user == activity.organizer` + `cohort == ADULT` (service already enforces the
ADULT-only wall; the action just exposes it). Return 200 + the updated serialized object.
Existing service already has the full guard; this is a thin DRF plumbing task.

**4. IndexNow heartbeat observability**
`apps/web/seo.py:submit_urls()` — after the `safe_get` call, count successes/failures, log a
structured summary line, and (if `settings.OPS_HEARTBEAT_URL` is set) POST
`{"status": "ok", "submitted": n, "failed": m}` via `apps.ops.heartbeat.ping_heartbeat()`.
`apps/ops/heartbeat.py` already exists for this pattern. ~10 lines.

### Medium builds (half-day to 1 day)

**5. Post provenance field (DSA Art.17 follow-up)**
Prevent a mod-remove overturn from resurrecting an author-self-deleted post.
- `apps/social/models.py` `Post`: add `is_author_deleted = models.BooleanField(default=False)`.
- `apps/social/services.py` `delete_own_post`: set `post.is_author_deleted = True` (alongside
  existing `post.is_hidden = True`).
- `apps/safety/services.py` `_reverse_action` (BAN/SUSPEND/REMOVE overturn): when un-hiding a
  `Post`, skip the `post.is_hidden = False` step if `post.is_author_deleted` (log instead).
- Migration: `social/0NNN_post_author_deleted.py` (boolean default=False, no data migration).
- Test: `test_overturn_remove_does_not_resurrect_author_deleted_post`.

**6. F19 safety record: remove cap on contesting beyond [:500]**
`apps/safety/services.py:safety_record_for()` — the current `[:500]`/`[:1000]` id-set caps mean
a user with many posts/activities can't contest a decision on content beyond the cap from the
pre-auth `/account/restricted/` surface.
Fix: replace the id-set approach with a direct `ModerationAction` query using GFK columns:
`ModerationAction.objects.filter(target_content_type=ct, target_object_id__in=Subquery(...))`
with a paginated outer query (e.g. `order_by("-created_at")[:200]` slices over the most recent
200 actions, enough to cover any realistic backlog while bounding the query). Test: add a user
with 600 posts and assert all sanction decisions are reachable in the serialized output.

**7. IaC hardening (four small sub-tasks)**
Each is a standalone commit on a `chore/iac-hardening` branch:
- `terraform/main.tf`: add `backend "s3" { bucket = var.tf_state_bucket ... }` block (or
  Terraform Cloud `backend "remote" {}`). Document the bootstrap step in `docs/HOSTING_EU.md`.
- `terraform/`: add an S3 backup lifecycle rule resource expiring objects after 90 days.
- `.github/workflows/ci.yml` (or `render.yaml` build command): add a lint step:
  `terraform fmt -check && terraform validate && tflint --chdir=terraform/`.
- `terraform/variables.tf`: fix `server_type` default `cpx21` → `cpx22` (matches README);
  update `docs/HOSTING_EU.md` to name terraform/cloud-init/tfvars.
No adversarial review needed (IaC/docs only); ruff is irrelevant; run `terraform validate` locally.

### CSP enforcement path (medium, sequence matters)

**8. Move CSP from report-only to enforced**
_Prerequisite_: let the report-only CSP run in prod for at least one week and review the violation
log at `apps.ops.csp_report` to find any remaining inline scripts.

Steps (on a `feat/csp-enforce` branch):
1. Grep `apps/web/templates/` for `<script>` without `src=` — any inline JS must move to an
   external file in `static/js/` (already done for `browse-modes.js` and `map.js`) or get a nonce
   via `{% csp_nonce %}` (django-csp provides this; ensure `CONTENT_SECURITY_POLICY["nonces"]`
   includes `"script-src"`).
2. Remove `"unsafe-inline"` from the `script-src` directive in `config/settings/base.py`.
3. Move the full directive dict from `CONTENT_SECURITY_POLICY_REPORT_ONLY` to
   `CONTENT_SECURITY_POLICY` (keep report-only dict for staging monitoring).
4. Add a browser-level test (Playwright or a simple GET-and-parse) asserting the prod-like response
   header is `Content-Security-Policy:` not `Content-Security-Policy-Report-Only:`.
Adversarial review focus: any path that emits `<script>…</script>` inline that would break on
enforcement (Leaflet, chart code, Django's admin, etc.).

### Deferred (needs a decision before touching)

**9. EUDI `_enforce_subject` — per-account holder-id anti-transfer**
`apps/accounts/identity/providers/eudi.py` — `_holder_id` always returns `None` today; the check
is a no-op. The `holder_hash` uniqueness via `IdentityBinding` already prevents same-wallet
re-registration across accounts. The missing piece is per-session subject verification (same wallet
presents the same `sub` claim). Blocked on whether EUDI wallet `sub` is stable across credential
renewal/key-rotation for our RP — a false rejection would lock out a legitimate user. Wait for EU
protocol clarity (~Q4 2026). `release_binding` (built) is the escape hatch once this lands.

**10. Multi-worker Prometheus aggregation**
`config/settings/base.py`, `config/urls.py` — the single-box launch is a deliberate SPOF; this
only matters at multi-dyno/worker scale. When scaling up: enable django-prometheus
`PROMETHEUS_MULTIPROC_DIR` (shared tmpfs across workers) or deploy a Pushgateway. Deferred until
scale is needed.

**11. Shared (Redis) circuit-breaker state**
`apps/ops/resilience.py` — current per-process `CircuitBreaker` works for a single-worker deploy;
a multi-worker deploy sees independent per-process breakers (no shared open/close signal).
When multiple workers are deployed: add `RedisCircuitBreaker` subclass storing state in Redis
(atomic `SET NX`/`GET` ops). Deferred until multi-worker scale.

**12. Covering indexes / systematic N+1 audit**
Correctly deferred — needs `EXPLAIN ANALYZE` on a prod-sized table before writing migrations.
A speculative `AddIndexConcurrently` on `Post` or `AuditLog` without evidence risks a slow
table scan at the worst time. When prod has meaningful traffic: `EXPLAIN (ANALYZE, BUFFERS)` the
slowest queries, then write targeted `AddIndexConcurrently` migrations with `INCLUDE` columns.

### Decisions needed (product / DPO / legal — no build yet)

**13. Progression "Level N of 5" gamification copy**
`apps/accounts/avatars.py:progression_level`, `apps/recommendations/services.py:progression_intensity`.
Currently self-only and never public (`PROGRESSION_AVATAR_PUBLIC` default off; tests pin it).
Decision: keep the "Level N" label (soft, no leaderboard, no nudges, upholds inv.2 because it's
purely self-visible) — or revert to a plain meetup count string. Either path is a 5-line change.

**14. Coarser anonymous public-card display**
`apps/web/templates/web/discover.html` — the anonymous `/discover/` page shows exact `start_time`
and named venue of adult meetups. Privacy call: should the public view show a rounded hour and
neighbourhood string instead? The ADULT-cohort wall is absolute; the question is granularity of the
displayed fields. Decision needed from product/DPO before touching the template.

**15. PhotoDNA / perceptual hash matcher for minors**
`apps/media/scanners.py` — SHA-256 blocklist is exact-only (any re-encode evades). A managed
perceptual CSAM scanner (PhotoDNA-class, e.g. Microsoft CSAM API, AWS Rekognition Custom Labels)
is the correct long-term solution for a child platform. Decision: vendor (cost, EU data residency,
DPA, SLA), then wire as a second `ManagedScanner` behind `MEDIA_CSAM_SCANNER_*` settings.
`MEDIA_REQUIRE_SCANNER=True` in prod enforces fail-closed until a scanner is configured.
