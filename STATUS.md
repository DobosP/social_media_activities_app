# Status — social_media_activities_app

Last verified: 2026-10-13

- **GitHub Actions (Last verified: 2026-10-05):** owner-requested on-demand policy, [ADR-0033](docs/adr/0033-manual-github-actions.md). Ordinary `ci.yml` calls Go/Node workflows;
  original Python gates remain in optional manual `reference.yml` (ADR-0038). YAML/input/job/read-only permission checks pass. Native Actions remains manually disabled; no hosted run claimed.

Current truth: STATUS > newest ADR > other docs. History: WORKLOG, ADRs and git.
## What this is

A children-first, in-person local-activities social app: no ads, deterministic
discovery rather than engagement ML, Cluj-Napoca first, EU residency required,
and donations only. `docs/SAFETY.md` owns the safety invariants.

## Product contract

- **RO-EDU canonical places/events (ADR-0023/0024/0055; Last verified: 2026-10-11):** the only canonical product accepted is
  `roedu:social_media_activities_app:events_places:v1`, schema 1. Pages require one immutable promoted release/snapshot
  identity, coherent completeness, safe bounded pagination, exact facts-only fields, exactly policy schema 5/ruleset 8/hash
  `140290d4bc6374ebb5b7922dee98c79bee5501a6cf76e1ef18923a8647068fd1` (4/6, 5/7 refused), capture/acquisition schema 3. Unknown
  fields, prose/person data, internal evidence/paths, unsafe URLs, policy drift, malformed relationships, duplicates, and page drift fail closed.
  Development/loopback-only local POC (ADR-0057; 2026-10-12): a verified admission over the item's REAL deny, honest privacy values, every POC row labelled "LOCAL POC — not cleared for publication"; production unchanged.
  City filters compare NFC/ASCII/cedilla spellings without rewriting facts; dev POC `past_only=1` reaches history separately from the bounded upcoming list (ADR-0059; 2026-10-13). Mode/form/SPA/feed links retain supported filters and POC labels.
- **Lifecycle and reconciliation:** source lifecycle/confidence/category, recurrence/timezone/price/availability,
  source timestamps, venue identity, and pack/release/snapshot identity are retained. One RO-EDU venue id is one place:
  same-location venues never merge (ADR-0058; item license ≤500 runes). Cancelled, postponed, removed, moved-online,
  tombstoned, low-confidence, or unsafe-venue events stay out of public discovery. Only an unbounded clean full snapshot
  can reconcile absence, atomically within its exact pack/city scope; partial/delta/legacy reads cannot.
- **Plural sentiment and moderation (ADR-0029):** fixed appreciation facets, adult-only dissent, private
  conduct concern, minor-protective thresholds, anti-pile-on/coordination sensors, batched public summaries,
  and audited human moderation are implemented for activity and group threads. Counts and engagement ranking
  are not exposed.
- **Private-thread media (ADR-0026):** canonical AVIF/WebP image processing and adult-only, cohort-gated
  private-thread video are implemented with fail-closed scanning, sandboxed transcoding, signed serving,
  retention/erasure coverage, and no public/discovery short-video surface.
- **Identity surfaces (ADR-0027/0028):** non-collectible signature-avatar styles, uniqueness enforcement,
  tiered profile visibility, block/cohort vetoes, and query-bounded hover cards are live. Minor pairs remain
  clamped.
- **Public/agent access (ADR-0025):** anonymous event/place APIs, gate-filtered snapshots with safe V2
  source facts, event price/availability JSON-LD, the no-DB Go sidecar, and crawler contracts are
  implemented. Public activity export remains the hard-coded ADULT + explicit-listing subset.
- **Core product/runtime:** D1–D10 and the audited feature waves, phased React/Preact UI behind kill
  switches, API/CSP/header/readiness hardening, bounded native HTTP/database behavior, EU-hosting templates,
  and deferred jobs are present. The production Terraform has never been applied.
- **Native Go serving backend (ADR-0032):** `services/server` now owns account/password/OAuth,
  EUDI/guardian/cohort gates, domain APIs, voting, moderation, media, encrypted live transport,
  HTML/SPA hydration, booking/donations, notifications/discovery, native schema adoption and jobs.
  Docker/Compose/systemd/Render/cloud-init launch paths invoke Go, but Render and fresh cloud-init cannot
  boot yet (RELEASE_READINESS §Before first deployment). Shared authentication is a hash-verified
  portable copy; Django remains offline contract/reference tooling.
- Go conversion on `origin/main` (2026-10-04): landing authorized by the owner 2026-10-04; human
  auth/erasure/privacy/safety code review pending — gates first deployment (ADR-0040).
  Social deployment/provider/minor launch gates remain separate from source landing.

## Safety and operating gates

- A RO-EDU venue remains child-venue **UNKNOWN** until staff approve that exact
  place. Low-confidence and non-live lifecycle rows remain nonpublic.
- Canonical ingestion never copies descriptions, people, private/internal
  evidence, internal paths, or raw provenance. License/access metadata survives.
- Cohort, guardianship, block, minor-contact, moderation, consent, and
  private-thread visibility gates are unchanged by the V2 integration.
- Landing needs green local gates on the exact head plus an independent reviewer (ADR-0040); it does not
  run ingestion, enable scheduled sync, deploy, apply Terraform, or authorize minors.
- First deployment is gated by human auth/erasure/privacy/safety code review and the list in
  `docs/RELEASE_READINESS.md` §Before first deployment (audit 2026-10-05).
- Launch is blocked on the GDPR/DPIA/DPO/parental-consent stack and production
  operations. Never apply paid infrastructure without owner authorization.
- Audit 2026-10-05 G2: report eligibility (ADR-0041), direct-only blocks/sanction chat eviction (ADR-0043),
  avatar/fingerprint minimisation (ADR-0044), guardian authority (ADR-0045), co-member logistics (ADR-0046),
  typing throttle, group mentions, message history/query ceilings and safe-exit API responses (WORKLOG).
- Audit 2026-10-05 G3: administrator-only console; failed-login/IP admission, bounded media/runtime and budget families;
  busy avatars return503 without spending an attempt. Two snapshot producers still differ (WORKLOG, GO-EXPORT-01).

## Open work

- REST assertion closure remains separate:6 social API declarations, booking assertions, export query equality and
  existing donation REST assertions; [bounded plan](docs/reviews/native-go/rest-contract-plan.md). Finance service/HTML gaps remain separate.
- Native completion/toolchain/audit stacks (ADR-0035/0038) are source-qualified per ADR-0040; human code review
  gates first deployment. The1651 unresolved frozen declarations keep Python source/tests and block retirement; [GUI capture prep](docs/reviews/gui-capture-preparation/README.md) remains unsigned/unrun.
- Build/promote a fresh immutable producer/server V2 release before real sync;
  the serving repo's producer dependency must be intentionally bumped first.
- Complete held-event review UX, curated cultural child-venue policy, localized
  taxonomy/cinema mapping, and production alerting/shared-state operations.
- Native shared-state/action limits use PostgreSQL (ADR-0037); typed policy overrides and private,
  optional Sentry reporting are implemented (ADR-0034). Redis/Python worker flags remain refused;
  external delivery, source landing and production activation still require their own verification.
- Operational gaps remain in `docs/PRODUCTION_READINESS.md`. Treat an unticked box
  in `docs/archive/COMPLETENESS_GAPS_2026-06.md` as a hypothesis to verify against
  HEAD, not a specification — two backlog surveys turned already-shipped entries
  back into planned work.
- Hosting provider and box size are RECOMMENDED-NOT-CONFIRMED (ADR-0001 §To revisit; `deploy/README.md` banner,
  owner note 2026-07-02): reconcile by ADR before procurement. Never `terraform apply` without owner go-ahead.

## Verification record (newest first)

- Event facets (2026-10-13, ADR-0060): reviewed source retained catalog54+web106/native31/Node4 proof; app inventory assertion corrected, isolated app35 PASS /0 SKIP/0 FAIL. First full21 stopped on that stale test (WORKLOG); corrected full21 pending. Owner app-main landing/push and activation remain pending.
- City/history (2026-10-13, ADR-0059): exact `f412dfc` **740 PASS / all21 lanes / 0 SKIP / 0 FAIL**; native/frontend/focused and negative controls pass. [Receipt](docs/reviews/native-go/local-poc-city-filters-checkpoint-20261013.json) pins unchanged source inputs; live POC remains separate.
- Policy 5/8 + local POC landing (2026-10-13): exact `98bc921` full native qualification **737 PASS / all 21 lanes / 0 SKIP / 0 FAIL**; check-native, harness17, docs/whitespace pass. Demo fixture clocks aligned; old main failure reproduced. [Receipt](docs/reviews/native-go/policy8-local-poc-checkpoint-20261013.json) pins source objects; docs-only closure reuses them unchanged.
- Venue identity + license 500 (2026-10-12, ADR-0058, + review fix: `ingest_places source=roedu` guarded too): check-native pass; jobs/commands/budgets/catalog/admin/web on disposable PG: new identity/race/license tests pass, only the known codec/demo reds; new-binary `--migrate-only` widens adopted and fresh DBs ([WORKLOG](WORKLOG.md)).
- Local POC (2026-10-12, ADR-0057): hermetic `go test -race ./...` + vet pass; jobs/web/cmd/catalog/commands on disposable PG: POC DB tests pass (review fixes: a complete POC snapshot never reconciles, event evidence stored, feeds labelled), failures only the known codec/demo reds also on the base commit; policy 5/8 (2026-10-11, ADR-0055) same, legacy client 43/43 ([WORKLOG](WORKLOG.md)).
- Thread access `874b437` (2026-10-06):714/all21 fresh tests,0skips/failures; native/hash/vet/race + Node4 + harness17 pass; [receipt](docs/reviews/native-go/rest-thread-access-checkpoint.json).
- Three mappings/new6-case test: outsiderPOST403/owner201, privateGET403/owner200, guardian ghostwrite403/ward-own201; persisted author/body and zero-write/privacy checks;4 sensitivity controls fail intentionally.
- Twenty-one exact mappings:1020 manifest claims/1651 unresolved/0invalid of2671; retirement exits1. Docs/all defects0; whitespace clean. Production/schema/dependencies/frontend unchanged.

## Standard verification
Native race/vet + shared-source hashes; database/codec contracts require explicit disposable fixtures
through `scripts/qualify-native.sh` (`docs/agent-testing.md`); its fail-closed gates are regression-tested by `scripts/test-native-gates.sh`. Whitespace/doc gates are required.
Dispatch-only (ADR-0033) Go/Node CI: `ci.yml`/`native.yml`/`go.yml`; optional offline reference: `reference.yml`; operators: CLI guide/ROEDU integration.

## Doc map

| Doc | Job |
|---|---|
| `AGENTS.md` | Operating contract: read first, commands, safety, docs discipline. |
| `docs/README.md` · `docs/agent-map.md` · `docs/agent-testing.md` | Full index · entry points and routes · gates. |
| `docs/PRODUCTION_READINESS.md` · `docs/RELEASE_READINESS.md` | Live gap list · launch gate and before-first-deployment list. |
| `docs/ROEDU_INTEGRATION.md` · `docs/SAFETY.md` | RO-EDU operator contract · child-safety invariants. |
| `docs/adr/` · `WORKLOG.md` | Decisions · dated history. |
| vault `projects/social-media-activities-app.md` | Fleet role, status, next. |
