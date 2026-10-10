# Status — social_media_activities_app

Last verified: 2026-10-10 (focused public GUI fixtures; prior broader receipts unchanged)

- **GitHub Actions (Last verified: 2026-10-05):** owner-requested on-demand policy,
  [ADR-0033](docs/adr/0033-manual-github-actions.md). Ordinary `ci.yml` calls Go/Node workflows;
  original Python gates remain in optional manual `reference.yml` (ADR-0038). YAML/input/job/
  read-only permission checks pass. Native Actions remains manually disabled; no hosted run claimed.

Current truth: STATUS > newest ADR > other docs. History: WORKLOG, ADRs and git.

## What this is

A children-first, in-person local-activities social app: no ads, deterministic
discovery rather than engagement ML, Cluj-Napoca first, EU residency required,
and donations only. `docs/SAFETY.md` owns the safety invariants.

## Product contract

- **RO-EDU canonical places/events (ADR-0023/0024):** the only canonical product accepted is
  `roedu:social_media_activities_app:events_places:v1`, schema 1. Pages require one immutable promoted
  release/snapshot identity, coherent completeness, safe bounded pagination, exact facts-only fields, policy
  schema 4/ruleset 6/hash `07f27d3c9a5e5898ba7cfac686c645713114dd9c13d72ecc054570d368daf58d`, and
  capture/acquisition schema 3. Unknown fields, prose/person data, internal evidence/paths, unsafe URLs,
  policy drift, malformed relationships, duplicates, and page drift fail closed.
- **Lifecycle and reconciliation:** source lifecycle/confidence/category,
  recurrence/timezone/price/availability, source timestamps, venue identity, and pack/release/snapshot
  identity are retained. Cancelled, postponed, removed, moved-online, tombstoned, low-confidence, or
  unsafe-venue events stay out of public discovery. Only an unbounded clean full snapshot can reconcile
  absence, atomically within its exact pack/city scope; partial/delta/legacy reads cannot.
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
  existing donation REST assertions; [bounded plan](docs/reviews/native-go/rest-contract-plan.md). Finance service/HTML gaps remain separate. [Public GUI fixtures](docs/gui-public-golden-fixtures.md):8b app63PASS/28equal diagnostics retained; two external-script nonce attributes + explicit original/expected/current delta checks authored NOT RUN (ADR0054). Original corpus/764binary unchanged; no fullM1/group/Live/templ/retirement acceptance.
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

- Thread access `874b437` (2026-10-06):714/all21 fresh tests,0skips/failures; native/hash/vet/race + Node4 + harness17 pass; [receipt](docs/reviews/native-go/rest-thread-access-checkpoint.json).
- Three mappings/new6-case test: outsiderPOST403/owner201, privateGET403/owner200, guardian ghostwrite403/ward-own201; persisted author/body and zero-write/privacy checks;4 sensitivity controls fail intentionally.
- Twenty-one exact mappings:1020 manifest claims/1651 unresolved/0invalid of2671; retirement exits1. Docs/all defects0; whitespace clean. Production/schema/dependencies/frontend unchanged.
- Prior [thread-pages713](docs/reviews/native-go/rest-thread-pagination-checkpoint.json), [own-list712](docs/reviews/native-go/rest-own-memberships-checkpoint.json), [RSVP711](docs/reviews/native-go/rest-rsvp-checkpoint.json), [Presence710](docs/reviews/native-go/rest-presence-checkpoint.json), [auth/transit709](docs/reviews/native-go/rest-auth-transit-checkpoint.json) and [transport709](docs/reviews/native-go/rest-transport-checkpoint.json) receipts remain; c27 image supplies codecs only.
- Original `c27a99f`705 [source/image/audits](docs/reviews/native-go/restart-checkpoint.json) remain; no new deployment-image/audit/frontend qualification. Unused openpgp advisory stays separate.
- Independent source/sensitivity review approved; final receipt/docs closure receives review before main push. Human deployment review remains open.

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
