# Security, Privacy & Child-Safety Audit — Social Activities Platform (`claude/wave1-prod-hardening`)

**Target:** Django 5.2 + DRF + PostGIS in-person-activity platform (Romania/EU, nonprofit, **includes minors**)
**Branch / HEAD:** `claude/wave1-prod-hardening` @ `9382e0d` ("Address adversarial review of the Wave-1/2 batch")
**Audit date:** 2026-05-29
**Method:** Source-traced (Bash/Read/Grep) + one live dev-runserver load/abuse session + targeted regulatory research. Findings are tagged **confirmed** (traced/executed), **likely**, or **theoretical**. The lead auditor independently re-verified every Critical/High and corrected two overstated claims (see §2 note on prod boot).

---

## 1. Executive Summary & Release-Readiness Verdict

The Wave-1/2 hardening is real and, in its cryptographic and service-layer core, genuinely good. The cohort-isolation chokepoint pattern (single service-layer gate per surface), the EUDI verifier's ES256-pinned signature checks, the single-use age nonce, the fail-closed photo-upload gate (off-by-default with `MEDIA_REQUIRE_SCANNER=True`), the donation-webhook auth, the decompression-bomb guard, the audit chain that survives erasure, the stored-XSS sanitization, the deny-by-default DRF permissions, and the eliminated feed N+1 + wired HNSW index all hold up under adversarial tracing. **No cross-cohort private-content read was achievable through the HTTP API.**

However, the audit found **two real-time child-safety failures, two cohort-isolation seams, an incomplete GDPR erasure, an effectively-disabled retention regime, and a large block of legally-mandatory artifacts that are absent but marked "done."** Several of these are launch-gating for a service onboarding minors in the EU.

### RELEASE-READINESS VERDICT: **NO-GO for a public beta with minors.**

This is a blocker verdict, not a quality verdict. The engineering is above average for this stage; the blockers are a mix of (a) a small set of high-severity runtime safety/consent bugs and (b) legally-mandatory pre-conditions for processing children's data that no amount of code quality substitutes for. Specifically:

1. **A removed / banned / consent-revoked user keeps receiving live messages** on an already-open WebSocket — including an adult whose guardian-observer status was revoked still reading a children's conversation (§2 F1, **Critical, confirmed**). This directly violates the platform's own "no consent → no access" invariant.
2. **No DPIA, no ROPA, no DPO, no processor DPAs, no breach runbook** — mandatory under GDPR Art. 35/30/37/28/33 *before* processing children's data — and the hardening plan **falsely marks these "✅"** (§4 L-DPIA, **Critical, confirmed-absent**).
3. **"Verifiable parental consent" is not verifiable**: any verified adult can become any minor's guardian on a mutual click, then self-grant consent and gain observer access to that child's E2EE chat (§2/§4 F-GUARDIAN, **Critical, confirmed**). This is the central grooming-prevention seam and the legal core of GDPR Art. 8.

### Top 5 Risks

| # | Risk | Severity | Status |
|---|------|----------|--------|
| 1 | Open WebSocket keeps delivering to a revoked/banned/consent-withdrawn user (no live-socket eviction; no per-delivery re-auth) | **Critical** | confirmed |
| 2 | Guardianship requires no proof of real parent-child relationship → invalid parental consent + adult observer access to a minor's E2EE chat | **Critical** | confirmed |
| 3 | Mandatory child-data compliance artifacts (DPIA/ROPA/DPO/DPA/breach) absent but mis-marked complete; no working production age-verification trust anchor exists in 2026 | **Critical/High** | confirmed-absent / uncertain |
| 4 | Cohort change on re-verification never evicts user from old-cohort conversations and the read path never re-checks cohort → adult keeps reading a children's chat | **High** | confirmed |
| 5 | Storage limitation effectively off (retention defaults 0, **no cron in render.yaml** so purges never run) + GDPR erasure leaves authored E2EE ciphertext orphaned & still decryptable | **High** | confirmed |

---

## 2. Technical Findings (severity-ranked, de-duplicated)

> **Lead-auditor correction (important):** Two streams stated that prod "fail-closes" / "would refuse to boot" without Redis. **This is wrong as the repo is configured.** `config/settings/prod.py:88-107` only `warnings.warn()` on LocMemCache/InMemoryChannelLayer **unless `DJANGO_REQUIRE_SHARED_STATE=True`**, and `render.yaml` does **not** set that flag. The documented deploy therefore **boots normally** on per-process backends. This makes the rate-limit-de-globalization and Redis findings *more* severe than stated, and removes the "deployment-blocking but safe" framing. The dev-provider and `EUDI_SANDBOX` assertions *do* hard-fail correctly.

| Sev | Title | Evidence (file:line) | Exploitability | Fix |
|-----|-------|----------------------|----------------|-----|
| **Critical** | **Revoked/removed/banned user keeps receiving live WS messages (no eviction, no per-delivery re-auth).** `chat_message`/`conversation_message` `send_json` to every channel in the group with no recheck; revocation/ban/REMOVE only mutate DB rows; repo-wide the only `channel_layer` use is inside the two consumers (no `group_discard`/`close` on any kick event). | `apps/chat/consumers.py:46-47`; `apps/messaging/consumers.py:46-47`; revoke paths `apps/messaging/services.py:476-495`,`455-472`; `apps/accounts/services.py:300`; `apps/safety/services.py:180-190` | confirmed | Re-run `can_view`/`can_access_thread` inside the `*_message` handler before delivery; add a `force_disconnect` group message fired from every revoke/ban/REMOVE/block path; consumers join a per-user control group for app-wide ban. |
| **Critical** | **Guardianship binds a child to any self-asserted adult on mutual click; no proof of legal parent-child relationship.** Invite checks only `guardian.cohort==ADULT`+`can_participate`; accept only re-checks adulthood; `grant_parental_consent` needs only `is_guardian_of`. Grants consent + read-only observer access to the child's E2EE chat. | `apps/accounts/services.py:99-114`, `136-173`, `255-285`; observer enroll `apps/messaging/services.py:382`; acknowledged `apps/accounts/models.py:217-226` | confirmed | Gate guardian-link ACTIVE + observer enrollment on a verified parental-responsibility attestation (EUDI guardian flow or admin-mediated out-of-band claim code). Until then disable minor onboarding in prod; stop labelling this "verifiable parental consent" in `COMPLIANCE.md`/models. |
| **High** | **Cohort change never evicts from old-cohort conversations; read path never re-checks cohort.** `apply_assurance` calls `recompute_cohort()` but never `remove_user_from_conversations`; `can_view` checks only active-participant + `can_participate`, *not* `user.cohort==conversation.cohort`. An adult keeps **reading** an ongoing children's conversation (send path *is* blocked at services.py:638). | `apps/accounts/services.py:23-30`; `apps/messaging/services.py:594-603`, `638` | confirmed | On cohort change, evict from non-matching conversations; make `can_view` fail closed on cohort for non-guardian participants (mirror the send-path check). |
| **High** | **GDPR Art.17 erasure leaves authored E2EE ciphertext orphaned & still decryptable.** `erase_user` only `target.delete()`; `Message.sender=SET_NULL` so the row + recipients' `MessageKey` survive. Cleartext D3 chat (CASCADE) deletes correctly — the inconsistency is the tell. | `apps/accounts/services.py:222-252`; `apps/messaging/models.py:140-145` | confirmed (row observed surviving with ciphertext) | In `erase_user`, before delete: `Message.objects.filter(sender=target).delete()` (or null ciphertext/iv) + `remove_user_from_conversations(target)`; regression test asserting no residual rows. |
| **High** | **Anonymous rate-limit bypass via spoofed `X-Forwarded-For` (`NUM_PROXIES` unset).** DRF keys the anon bucket on the full client-controlled XFF behind Render's edge proxy → every AllowAny endpoint effectively unlimited (discovery feeds, places `page_size=500`, donation row-creation, `/healthz`). | no `NUM_PROXIES` anywhere (grep empty); `config/settings/base.py:171-178`; `render.yaml` runs daphne behind edge proxy | confirmed | `REST_FRAMEWORK['NUM_PROXIES']=1` or custom `get_ident` trusting only the last hop; test that two forged-XFF requests share a bucket. |
| **High** | **No app-layer brute-force/lockout on web `/login/`.** Stock Django `LoginView`, not a DRF view → DRF throttles don't apply; no `django-axes`/ratelimit. Combined with the XFF bypass there is no effective per-IP brake. Minors' accounts. | `apps/web/urls.py:10`; no axes/ratelimit (grep) | confirmed | Add `django-axes` or username+real-IP login throttle with lockout. |
| **High** | **Unbounded JSON request bodies on AllowAny DRF write endpoints (memory-amplification DoS).** 5MB & 10MB JSON POSTs to `/api/donations/` returned **201** — `DATA_UPLOAD_MAX_MEMORY_SIZE` not set and does not cover DRF's `JSONParser`; no edge body cap. Single daphne process holds all memory. | `apps/donations/views.py:18-38`; no `DATA_UPLOAD_MAX_MEMORY_SIZE` (grep empty) | confirmed (probed) | Reject `Content-Length` over a cap in middleware before DRF reads the stream; set `DATA_UPLOAD_MAX_MEMORY_SIZE`. |
| **High** | **EUDI verifier performs no holder-binding / proof-of-possession; `sub` never bound to the requesting user.** Verifies only issuer ES256 + aud + exp + nonce-in-credential. Safe *today* only because the server-generated single-use nonce is user-bound; wiring real wallet credentials would let a leaked/shared credential be presented by another within the window. Also cannot interoperate with real wallets (a real issuer never embeds the verifier nonce). | `apps/accounts/identity/eudi/verifier.py:19-50`; `providers/eudi.py:82-96` | likely | Verify a holder key-binding JWT (cnf/`sub`) over aud+nonce before trusting claims; bind `sub` to a stable per-user holder id. |
| **High** | **W1-3 missing: no `EUDI_TRUSTED_ISSUERS` non-empty assertion; CI `check --deploy` passes GREEN with an empty trust anchor.** Prod can boot with `IDENTITY_PROVIDER=eudi`, `EUDI_SANDBOX=False`, and `EUDI_TRUSTED_ISSUERS={}` → every verification fails closed forever (no minor can be onboarded) with no boot/CI signal. Plan claims this guard exists; it does not. | `EUDI_TRUSTED_ISSUERS` default `{}` `config/settings/base.py:148`; `config/settings/prod.py` assertions cover only dev-provider+sandbox; CI never sets the var | confirmed | Add a prod boot assertion / system check: non-DEBUG + EUDI + not-sandbox ⇒ `EUDI_TRUSTED_ISSUERS` non-empty & well-formed PEM; set it in CI's deploy check. |
| **Medium** | **SSRF in iCal/Overpass/Wikidata/Google/managed-scanner fetchers** — no internal-IP guard, `allow_redirects` defaults True, no size cap; iCal fetches an operator-supplied URL and the response lands in user-rendered Event rows (semi-blind SSRF + unbounded-memory DoS). | `apps/events/sources.py:116`; `apps/ingestion/sources/overpass.py:89`; `apps/places/enrichment/wikidata.py:46`,`google.py:44,59`; `apps/media/scanning.py:97`; no `ipaddress`/`allow_redirects` anywhere | confirmed (operator-triggered) | Shared safe-fetch helper: reject private/loopback/link-local/reserved IPs, `allow_redirects=False` (re-validate each hop), `stream=True` + max-bytes, truncate `description`. |
| **Medium** | **Per-process rate-limit de-globalization at scale.** As corrected above, prod boots on LocMemCache by default (`render.yaml` lacks `REDIS_URL` and `DJANGO_REQUIRE_SHARED_STATE`). Any 2nd process/instance multiplies every cap (anon/user throttles, messaging/chat caps, guardian-invite cap, report cap) by N and resets on restart — anti-grooming send-rate limits unenforceable. Caps horizontal scale. | `render.yaml:34-49` (no REDIS_URL); `config/settings/prod.py:88-107` (warn-only) | confirmed | Add managed Redis + `REDIS_URL`; set `DJANGO_REQUIRE_SHARED_STATE=True` so multi-process boots fail closed; document in OPERATIONS. |
| **Medium** | **Sender-side recheck doesn't enforce blocking between active group members.** `is_blocked` gates new conversations/discovery but live group fan-out still delivers between two members who blocked each other after joining. | `apps/messaging/services.py:130-149`, `617-718`; `apps/chat/services.py:36`; fan-out `consumers.py:46` | likely | Per-recipient delivery filtering on `is_blocked`; define policy on block-in-shared-group. |
| **Medium** | **WS auth is session-cookie-only, resolved once at connect; ban/logout/erasure does not sever an open socket.** Emergency ban of a predator account does not take effect in real time on existing sockets. | `config/asgi.py:21-28`; `apps/safety/services.py:182-184` | confirmed | Re-fetch/re-validate user per delivery; force-close via per-user control group on ban/logout/erasure. |
| **Medium** | **ModerationReportListView returns the entire `Report` table** (bare APIView, no pagination/cap) → self-inflicted DoS that worsens when moderation is busiest. Same unbounded-queryset pattern in conversation lists, guardian-observable conversations, my-donations, activity `mine`, posts GET, ward list. | `apps/safety/views.py:88-93`; `apps/messaging/views.py:93-95`,`240-244`; `apps/social/views.py:121-129`,`143-144`; `apps/donations/views.py:44-46`; `apps/accounts/views.py:67-72` | confirmed | Hard slice/pagination (mirror notifications `[:100]`); prioritize posts feed + conversation lists. |
| **Medium** | **Unbounded post/activity text bodies.** `Post.body`/`Activity.description` are `TextField()` with no `max_length`; serializers uncapped → ~2.5MB bodies at 240/min, amplified by the unpaginated posts feed. Chat & messaging ciphertext *are* capped — these were missed. | `apps/social/models.py:152`,`39`; `apps/social/serializers.py:74-80`,`57` | confirmed | Add `max_length` to serializers + enforce in services. |
| **Medium** | **`/healthz` shares the global anon throttle** → readiness probe gets 429'd under shared-IP/proxy load → Render flaps the instance. | `apps/ops/views.py:14` (no `throttle_classes`) | confirmed (probed) | `throttle_classes=[]` on HealthView. |
| **Medium** | **Self-declared `age_band` still in web sign-up; register view 500s in prod under EUDI and leaves an orphan account** (no `@transaction.atomic`, uncaught `IdentityVerificationError`). Net effect fail-closed (no age bypass) but the primary web onboarding path is broken in prod. | `apps/web/forms.py:27-35`; `apps/web/views.py:80-86` | confirmed | Remove self-declared field; wrap register in atomic; catch the error and route to the verify-age step. |
| **Medium** | **Admin bulk "dismiss" bypasses the service layer** → no tamper-evident audit row (and, once built, no reporter notice). `ban_target` correctly routes through `take_action`, exposing the inconsistency. | `apps/safety/admin.py:21-25` vs `27-42`; `services.dismiss_report:213-221` | confirmed | Route dismiss through `services.dismiss_report` per report. |
| **Low** | Report endpoint accepts any object by raw PK with no visibility check → cross-cohort existence oracle + report-flooding. | `apps/safety/views.py:36-57`; serializer `apps/safety/serializers.py:13-17`; contrast web `apps/web/views.py:620-629` | confirmed | Mirror web gating; return 404 on failure; accept `public_id`. |
| **Low** | 403/400-vs-404 existence oracles over sequential integer PKs (messaging/media/social); `MembershipViewSet` queryset spans the caller's whole cohort → enumerable membership graphs of minors. | `apps/messaging/views.py:144…292`; `apps/media/views.py:58-71`; `apps/social/views.py:153-156` | confirmed | Uniform 404 (the `UserKeyView:51` already does this correctly); scope `MembershipViewSet` to activities the caller belongs to. |
| **Low** | Overpass QL injection via operator `--city`; open redirect via unvalidated `next` in block/unblock; `SECURE_CONTENT_TYPE_NOSNIFF` unset; no `Cache-Control: private, no-store` on the children's-photo endpoint; username enumeration on register + messaging lookups; unverified↔unverified can view each other's photos (`cohort==UNASSIGNED` matches); `AgeAssurance.raw` editable/visible in admin; TOCTOU in the get-then-incr rate limiter; key-fingerprint canonicalization mismatch (server full-JWK vs client kty/crv/x/y); `unblock_user`/`lift_expired_suspensions` call `select_for_update` outside a transaction (500). | overpass `:80`; `apps/web/views.py:673,682`; no `NOSNIFF`; `apps/media/views.py:96-107`; `apps/web/views.py:77-78`,`apps/messaging/views.py:111-116,169-171`; `apps/media/services.py:126-136`; `apps/accounts/admin.py:56-60`; `apps/safety/services.py:249-259`; `apps/messaging/services.py:84-91` vs `static/js/e2ee-messaging.js:130-141`; `apps/safety/services.py:96-99,218,242` | confirmed/likely | See per-item fixes in the detailed list above; all are low-blast-radius hardening. |

### Verified SOUND (checked, not a finding)
- **EUDI alg-confusion / `alg=none`** — defeated by PyJWT `algorithms=["ES256"]` allowlist (verified empirically).
- **Single-use age nonce** — server-generated, user-bound via signed state, `ConsumedAgeNonce` blocks replay & re-redemption.
- **Donation webhook** — fail-closed, constant-time HMAC over raw body, replay window.
- **Image pipeline** — full re-encode strips EXIF/GPS; decompression-bomb guard (byte → header-pixel → decode order) is correct; UPLOAD-1 "hash the re-encode" no-op bug is **fixed** (now hashes original bytes, `apps/media/services.py:68-69`).
- **Stored XSS** — `safe_external_url`/`safe_href` strip script/protocol-relative; template filter returns non-safe string (Django auto-escapes); live-chat JS uses `textContent`; no `|safe`/`mark_safe`/autoescape-off.
- **SQL/geo/pgvector** — all ORM-parameterized with `float()` coercion; no raw SQL on user input; regexes linear (no ReDoS).
- **Feed N+1** — eliminated; constant 2 queries for 1/5/30 activities. **HNSW index** wired to `vector_cosine_ops`, used when beneficial.
- **Audit chain** survives erasure via `actor_ref`/`erased_public_id`; no email/birthdate stored on User.

---

## 3. Load / Stress Results (dev-runserver — RELATIVE, not prod-representative)

**Setup caveat:** Django dev runserver in docker-compose, single source IP. Prod runs a **single daphne ASGI process** (`render.yaml`), no gunicorn worker pool, no replicas (free plan). Absolute rps/latency are dev numbers; the *shape* is prod-representative. The single source IP means the 60/min anon throttle is the dominant 429 wall in API scenarios (realistic for any single-IP source). The live container's DB was **behind on Wave-1 migrations** (`is_hidden`, HNSW, age-nonce, guardian-invite) until `migrate` was run manually — a deploy-ordering finding.

### Scenario metrics

| Scenario | Conc. | Reqs | rps | p50 ms | p95 ms | max ms | Notes |
|----------|-------|------|-----|--------|--------|--------|-------|
| healthz | 10 | 1000 | 290.5 | 32.2 | 47.2 | 94.5 | 200×60 then 429 (anon bucket) |
| healthz | 50 | 1500 | 309.8 | 152.7 | 211.3 | 251.4 | 429 (bucket drained) |
| healthz | 100 | 2000 | 327.3 | 301.4 | 353.0 | 375.1 | latency ~3× c10, flat rps ⇒ serialized backend |
| places | 10 | 800 | 295.7 | 32.1 | 42.1 | 91.8 | first 60=200 then 429 |
| places | 50 | 1000 | 316.2 | 151.6 | 204.2 | 229.1 | |
| places | 100 | 1000 | 314.9 | 314.9 | 371.0 | 399.4 | |
| taxonomy categories | 50 | 1000 | 306.8 | 152.6 | 211.3 | 243.0 | |
| taxonomy activities | 50 | 1000 | 318.5 | 147.8 | 211.2 | 239.3 | |
| **home / (un-throttled Django view)** | 50 | 1000 | 315.3 | 150.8 | 213.2 | 234.1 | 200×1000 — best raw-throughput signal |
| home / | 100 | 1000 | 316.1 | 311.3 | 363.3 | 413.5 | rps flat, p50 doubles ⇒ single effective worker |

**Zero 5xx / connection errors / timeouts in any scenario.**

### DB probes
- **Feed N+1:** CONFIRMED eliminated — constant 2 queries for 1/5/30 activities (`with_counts` annotations, `apps/social/services.py:69`).
- **HNSW:** CONFIRMED index `actemb_vector_hnsw` exists (`hnsw(vector vector_cosine_ops) m=16 ef_construction=64`), selected with `enable_seqscan=off`; planner correctly prefers seqscan at 30 rows. (Approximate — `ef_search` not tuned.) **Caveat:** the recommendations query filters the HNSW table by `activity__in=<unbounded candidates>`, which can defeat the ANN guarantee at scale (`apps/recommendations/services.py:53-58`).
- **Page-size cap:** CONFIRMED — with 650 rows, `?page_size=100000` returns exactly 500; `?limit` ignored (`CappedGeoJsonPagination.max_page_size=500`).
- **Anon throttle:** CONFIRMED fires at 60/min and resets (LocMem, per-process). **(Recall: prod also runs LocMem by default — §2 correction.)**
- **Multi-MB JSON body:** CONFIRMED NOT rejected — 5MB & 10MB POSTs to `/api/donations/` returned 201.
- **DB guards:** dev `statement_timeout=0`; prod-only `statement_timeout=30000`, `CONN_MAX_AGE=60`, `CONN_HEALTH_CHECKS=True`. Staging should mirror prod OPTIONS.

### Empirical bottleneck ranking — what breaks first
1. **Single-process serialization (first to break).** rps pinned ~300–320 while p50 scales linearly (32→152→311 ms at c10/50/100); un-throttled `/home` identical. One slow PostGIS/HNSW/30s-timeout query stalls the whole instance. No worker pool, no replicas.
2. **Unbounded JSON bodies (high, confirmed).** A few 10MB bodies balloon RSS / OOM the single process; bounded only by 60/min/IP (and that bound is bypassable via XFF — §2).
3. **Health-probe fragility (medium).** `/healthz` shares the anon bucket → 429 → Render flaps the instance.
4. **DB connections (low for current shape).** `max_connections=100` matters only for a multi-worker deploy; the single daphne holds few connections.

### What breaks at 100 / 1k / 10k users
- **~100 concurrent:** functional but latency already climbs linearly; one slow query degrades everyone. Acceptable for a closed pilot.
- **~1k:** the single process saturates; p95 unacceptable; an attacker (XFF-rotated, unbounded bodies) can OOM/flap with modest effort. Needs multi-worker + Redis + body caps first.
- **~10k:** not survivable on the documented blueprint. Requires multiple ASGI workers + replicas, Redis (with `DJANGO_REQUIRE_SHARED_STATE`), paid Postgres with connection budgeting, body-size caps, paginated list endpoints, and an HNSW candidate-set rework.

---

## 4. Legal / Compliance Findings

> **Honest 2026 regulatory notes (auditor-flagged, require a Romanian lawyer/DPO to confirm — do not treat as legal advice):**
> - **ePrivacy voluntary-detection derogation** reportedly **lapsed ~3 Apr 2026** (EP rejected extension 26 Mar 2026; a separate extension to Aug 2027 also failed). If so, there is currently **no harmonised EU legal basis to scan message content** — making the app's no-scan-on-E2EE posture *correct*, and adding scanning now a *risk*.
> - **CSAR** remains in trilogue (resumes ~4 May 2026, target ~July 2026); **no mandatory client-side scanning is adopted**.
> - **EUDI wallets** due ~24 Dec 2026 with uneven member-state readiness (RO building a national wallet, not yet live). The EU age-verification app was **publicly bypassed in ~2 min (Apr 2026)**. ⇒ **No reliable production age-verification trust anchor exists today.**
> - **Romania "Online Age of Majority" / Digital Majority Law (L190/2025):** adopted by the Senate 6 Oct 2025; latest sources (Nov 2025) show it pending the Chamber of Deputies. **Could NOT confirm final adoption/promulgation/in-force by 2026-05-29** — a hard blocker to confirm. RO digital age of consent is **16** (Law 190/2018, GDPR Art. 8).
> - **DSA Art. 18** (notification of suspicion of criminal offences) applicability/scope to this nonprofit needs RO-counsel confirmation.
> - **Enterprise-size classification** (micro/small) drives DSA transparency-report/trusted-flagger exemptions — confirm with counsel.

| ID | Gap | Regulation / Article | Current state | Blocking for beta? | Remediation (owner) |
|----|-----|----------------------|---------------|--------------------|---------------------|
| L-DPIA | DPIA, ROPA, DPO appointment, processor DPAs, breach runbook, lawful-basis map **absent but marked "✅"** | GDPR Art. 35, 30, 37, 28, 33/34 | No artifacts exist (verified); plan mis-marks done; privacy policy + DPO contact are placeholders | **YES (hard)** | DPO/legal: complete all; appoint+register DPO with ANSPDCP. Eng: correct the false "✅" marks. |
| L-GUARDIAN | "Verifiable parental consent" not verifiable (any adult → any minor) | GDPR Art. 8 ("reasonable efforts"); RO Online-Majority law | `apps/accounts/services.py:99-114,255-285` checks only adulthood | **YES (hard)** | Eng: bind to verified parental-responsibility (EUDI/eID/admin claim-code). DPO: document method. Disable minor onboarding until done. |
| L-ANCHOR | No reliable production age-verification trust anchor (EU app bypassed; EUDI not live) | RO Online-Majority law; DSA Art. 28 | `EUDI_TRUSTED_ISSUERS={}`; sandbox blocked in prod | **YES (likely)** | DPO/product + RO counsel: decide interim eID for a closed pilot; define "reasonable efforts" for the wallet gap. |
| L-ROLAW | RO Online-Majority law final text / in-force date unconfirmed | RO L190/2025 | Pending Chamber per latest sources; fines 0.1–0.4% of national turnover (ANCOM/ANSPDCP) | **YES (likely)** | RO lawyer: confirm in-force status, consent standard, 180/120-day transition mechanics. |
| L-ERASE | Art.17 erasure leaves authored E2EE ciphertext + creator/report/audit references | GDPR Art. 17 | `erase_user` only `target.delete()`; `Message.sender=SET_NULL` | **YES** | Eng: delete/pseudonymize authored Messages+keys; call `remove_user_from_conversations`. DPO: document Art.17(3) carve-outs for safety records. |
| L-RETENTION | Storage limitation effectively off: retention defaults 0 **and no cron runs purges**; suspensions never auto-lift | GDPR Art. 5(1)(e), 25; DSA Art. 17 (proportionality) | `CHAT/MESSAGING_RETENTION_DAYS=0`; `render.yaml` has **no cron service** | **YES** | Eng/infra: add Render cron for `purge_messaging`/`purge_chat`/`lift_suspensions`; set non-zero defaults. DPO: define periods. |
| L-CSAM-SOP | No CSAM reporting SOP (named hotline/INHOPE/IGPR, chain-of-custody, timelines, responsible person) | DSA Art. 18; RO criminal-reporting duty | Only a 5-line generic RUNBOOK note; plan marks ❌ | **YES** | DPO/legal + moderation lead: write SOP (Salvați Copiii *Esc_ABUZ*/INHOPE + IGPR; no re-download; hash+preserve; named on-call). CSAM can still arrive via report-with-decryption even with uploads OFF. |
| L-TRANSFER | No int'l-transfer disclosure / SCCs; media S3 region not EU-enforced (`MEDIA_S3_REGION` default ""); Stripe (US) configured | GDPR Ch. V (Art. 44-46), Art. 13(1)(f) | Only web+DB pinned to `frankfurt`; bucket region unvalidated | **YES** | Eng: prod boot assertion that S3 region is EU. DPO: SCCs/DPAs with Stripe + storage + EUDI + Sentry; list sub-processors in policy. |
| L-POLICY | Privacy Policy & Terms are non-binding **DRAFTS** (placeholder controller/DPO, no retention, no transfer/cookie sections) | GDPR Art. 12-14; DSA Art. 14; ePrivacy Art. 5(3) | `privacy.html`/`terms.html` DRAFT banners + `dpo@example.org` | **YES** | DPO/legal: finalize binding text with real entity, DPO, retention, transfers, cookies. |
| L-DSA-16 | No receipt acknowledgement or decision-outcome notice to reporters | DSA Art. 16(1) & 16(5) | `file_report` & `ResolveReportView` never notify reporter; flash only | **YES** | Eng: emit Notification to reporter on file + on resolution; non-account notice channel. |
| L-DSA-20 | No internal appeal/complaint endpoint, **but Terms promise one** | DSA Art. 20 | No appeal route anywhere; SoR says "you may contest" with empty url | **YES** | Eng: Appeal model + endpoint + human-review queue. Or remove the promise from Terms until built. |
| L-DSA-17 | Statement-of-reasons omits mandatory Art.17(3) elements (territorial scope, legal-vs-ToS basis, automated-means, facts, redress avenues) | DSA Art. 17(3) | `apps/safety/services.py:153-156` gives only action+reason+"contest it" | NO (but pre-launch) | Eng: extend SoR composition; structured `StatementOfReasons` record for transparency reporting. |
| L-PORT | Art.20 portability not implemented despite policy promise | GDPR Art. 20 | No export endpoint | NO (but promised) | Eng: `GET /api/accounts/me/export/` (+ ward variant). Or soften policy wording. |
| L-BACKUP | Free Postgres has no backups; RUNBOOK promises nightly dumps; referenced `docs/OPERATIONS.md` missing | GDPR Art. 32(1)(c), 5(1)(f) | `render.yaml` free plan; OPERATIONS.md absent | NO (pre-launch) | Eng/owner: paid DB or external pg_dump→EU bucket; create OPERATIONS.md; rehearse restore. |
| L-DSA-MISC | No transparency report / Art.11-12 SPOC / trusted-flagger handling | DSA Art. 11/12/15/22/24 | None in code/docs | NO (size-dependent) | Counsel: confirm size class; publish Art.11/12 contact; defer the rest. |
| L-SCAN-DRIFT | Plan claims a "CI guard that prod never resolves an ineffective scanner" — **no such guard exists** | (internal control) | No prod assertion / CI test; runtime rejection only | NO (false-confidence) | Eng: prod assertion `MEDIA_REQUIRE_SCANNER and get_scanner().is_effective()`; correct plan status. |
| L-NIS2 | NIS2 applicability undetermined | NIS2 / EU 2022/2555 (RO transposition) | Marked ❌; likely out of scope at beta | NO | Counsel: short applicability memo; revisit at scale. |
| L-ART28 ✅ | **Art.28 conformance (no profiling ads, high default privacy) is REAL** — only the written evidence memo is missing | DSA Art. 28(1)/(2) | No ads/tracking/profiling code; content-based cohort-scoped recs; conservative defaults | NO (positive) | DPO: write the Art.28 evidence memo. |

---

## 5. Prioritized Remediation Plan

### P0 — Launch blockers (must clear before any public beta with minors)

| Item | Owner | Effort |
|------|-------|--------|
| Live-socket eviction + per-delivery re-auth in both consumers; force-disconnect on revoke/ban/REMOVE/block (§2 F1) | Eng | M (2–4 d) |
| Bind guardianship to verified parental responsibility; gate consent + observer enrollment; disable minor onboarding until done (L-GUARDIAN) | Eng + DPO | L (1–2 wk + legal) |
| Complete DPIA, ROPA, processor DPAs, appoint DPO, breach runbook; correct false "✅" marks (L-DPIA) | **DPO/legal** | L (weeks, external) |
| CSAM reporting SOP (named hotline/IGPR, chain-of-custody) (L-CSAM-SOP) | DPO/legal + mod lead | M (days, external) |
| Confirm RO L190/2025 in-force status + parental-consent standard; resolve trust-anchor strategy for the EUDI gap (L-ROLAW, L-ANCHOR) | **RO lawyer** | M (external) |
| Finalize binding Privacy Policy + Terms (entity, DPO, retention, transfers, cookies); remove appeal promise or build appeal (L-POLICY, L-DSA-20) | DPO/legal (+ Eng for appeal) | M–L |
| Complete erasure of authored E2EE ciphertext + `remove_user_from_conversations` (L-ERASE) | Eng | S (1 d) |
| Add cron (purge_messaging/purge_chat/lift_suspensions) + non-zero retention; EU-region assertion for S3; SCCs disclosure (L-RETENTION, L-TRANSFER) | Eng + DPO | S–M |
| Cohort-change eviction + `can_view` cohort fail-closed (§2 F-cohort) | Eng | S (1 d) |
| DSA Art.16 reporter ack + outcome notices (L-DSA-16) | Eng | S–M |
| `NUM_PROXIES=1`; login lockout (axes); request-body size cap (§2 — XFF bypass, brute-force, JSON-DoS) | Eng | S (1–2 d) |

### P1 — Pre-launch hardening / scale
- Redis + `DJANGO_REQUIRE_SHARED_STATE=True`; multiple ASGI workers + replicas (Eng, M).
- Paginate/slice all bare-APIView list endpoints; cap post/activity body length (Eng, S–M).
- SSRF safe-fetch helper across all external fetchers (Eng, M).
- EUDI holder-binding (KB-JWT/`sub`) + `EUDI_TRUSTED_ISSUERS` non-empty assertion + CI guard; scanner-effectiveness prod assertion (Eng, M).
- DSA Art.17(3) full statement-of-reasons; admin dismiss → service layer (Eng, S–M).
- `/healthz` throttle exemption; paid DB + backups + OPERATIONS.md; staging mirrors prod DB OPTIONS (Eng/owner, S–M).
- Remove self-declared `age_band`; atomic register; catch verify error (Eng, S).
- Art.28 evidence memo; NIS2 applicability memo; Art.11/12 SPOC (DPO/counsel, S).

### P2 — Defense-in-depth / consistency
- Uniform 404 for all existence oracles; `public_id` for externally-referenced user objects; scope `MembershipViewSet` (Eng, S).
- Report endpoint visibility gating; block-in-shared-group policy + per-recipient delivery filter (Eng, S).
- `SECURE_CONTENT_TYPE_NOSNIFF`, `Cache-Control: private, no-store` on media; `next` open-redirect guard; Overpass `--city` escaping; admin `AgeAssurance.raw` readonly (Eng, S).
- Atomic-transaction fix for `unblock_user`/`lift_expired_suspensions`; key-fingerprint canonicalization parity; rate-limiter TOCTOU → atomic INCR (Eng, S).
- Art.20 portability export endpoint (Eng, M).
- Recommendations HNSW candidate-set rework + short-TTL cache (Eng, M).

---

## 6. Open Questions for the Maintainer

1. **RO L190/2025 status (critical):** Is the Online Age of Majority law in force as of 2026-05-29, and what is its exact "verifiable parental consent" verification standard? (Could not confirm from public sources — needs RO counsel.)
2. **Trust anchor strategy:** Given the EU age app is bypassed and EUDI wallets are not live until ~Dec 2026, what is the plan for the gap — closed pilot with national eID, or delay minor onboarding?
3. **Guardian relationship proof:** Which mechanism will establish *real* parent-child relationship pre-EUDI (admin-mediated claim code, eID attestation, out-of-band)?
4. **Why are L-13/14/16/17/18 marked "✅" in `PRODUCTION_HARDENING_PLAN_2026-05.md` when no artifacts exist?** This false-readiness signal should be reconciled before any go/no-go review relies on the plan.
5. **Prod deploy shape:** Is the documented single-daphne, no-Redis, no-cron `render.yaml` the actual intended production config? If so, rate limits de-globalize on any scale-out and retention/suspension jobs never run — confirm the gap is understood (it boots with a *warning*, not a hard failure).
6. **DSA Art.18 / enterprise-size classification:** Does this nonprofit fall under Art.18 criminal-notification duties and the micro/small DSA exemptions? (Counsel.)
7. **Managed scanner & uploads:** Will uploads stay OFF for beta? If a `ManagedHttpScanner` is ever enabled, the lawful basis for CSAM hash-matching post-3-Apr-2026 and an EU-resident endpoint + DPA must be confirmed first.
8. **Migrations gating:** Does `preDeployCommand: migrate --noinput` actually gate traffic on every deploy? The live container was running behind on Wave-1 migrations during this audit.

---

*Confidence: Critical/High findings are source-verified at the cited file:line by the lead auditor (several also runtime-confirmed). Legal in-force dates and DSA applicability are flagged uncertain and require a qualified Romanian lawyer/DPO — they are not legal advice. Load numbers are dev-runserver, single-IP — treat as relative behavior, not prod capacity.*
