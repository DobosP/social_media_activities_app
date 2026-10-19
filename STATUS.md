# Status — social_media_activities_app

Last verified: 2026-10-20 (native pages through notification settings: complete affected PG6 and desktop/a11y qualified)

- **GitHub Actions (Last verified: 2026-10-05):** owner-requested on-demand policy,
  [ADR-0033](docs/adr/0033-manual-github-actions.md). Ordinary `ci.yml` calls Go/Node workflows;
  original Python gates remain in optional manual `reference.yml` (ADR-0038). YAML/input/job/
  read-only permission checks pass. Native Actions remains manually disabled; no hosted run claimed.
Current truth: STATUS > newest ADR > other docs. History: WORKLOG, ADRs and git.
Family pages: [ADR-0061](docs/adr/0061-native-family-pages.md) implements two typed Go views. Exact source118fd7f passes six complete affected PG packages:395roots/1,149named passes, zero skips/failures, six guarded database retirements/checkpoints and13owned-container cleanup. Eight desktop/two no-JS fixtures,192Axe predicates and27 button samples pass; [compact qualification](docs/reviews/gui-native-family/README.md) retains failures and source joins. Native HTML/allocations are smaller; body CPU remains higher than original Pongo. No full21/current-image/main/milestone/deployment claim. Group creation: [ADR-0062](docs/adr/0062-native-group-create.md) is independently feature-qualified: complete affected PG1f8c974/6packages/408roots/1,190named/zero skips plus scoped opaque-header presentationd05343b; all runtime Go unchanged. UI10states/30strictAxe/2no-JS/40PNGs and eight byte-exact family bodies pass; [keeper1bb27df9](docs/reviews/gui-native-group-create/README.md) retains exact source epochs, failures, cleanup and higher CPU/allocation-count measurements. No later exact-source completePG/full21/current-image/main/milestone/activation claim. Group detail: [ADR-0063](docs/adr/0063-native-group-detail.md) is independently feature-qualified on exactfa3e7c9: affected PG6/428roots/1,246named/zero skips, UI7+7/21strictAxe/18old bodies/native confirmations/cleanup; [keeperf1fc/acceptance948f](docs/reviews/gui-native-group-detail/README.md) preserves all failures and105 historical aliases. Matched body CPU is near-equal EN/modestly lower RO,allocated bytes/HTML/gzip lower but allocation count higher. Prior retention pruned780,572,285B; later Notification retention supersedes/prunes fa3 generated source/binaries. e900 is the rolling setup; Group-detail logs/UI/Git refs/keeper remain. Notification preferences: [ADR-0064](docs/adr/0064-native-notification-preferences.md) is independently feature-qualified on exacte900960 (4ad6/f332): complete affected PG6/436roots/1,279named/zero skips, UI6+6/18strictAxe408predicates/12keyboard Save states/25old bodies/cleanup; [keeper29a9ff](docs/reviews/gui-native-notification-preferences/README.md) retains1,484members/133historical aliases and immutable failed history. Current self-only services and mandatory delivery remain unchanged. Matched elapsed body timing44%EN/42%RO and allocations6.82% higher; allocated bytes26–27%, HTML4.5–4.7% and gzip9.5–10.3% lower. No CPU-profile/overall-speedup/full21/current-image/main/milestone/activation claim. Verified superseded owned outputs559,474,592B pruned; deleted raw paths unavailable, latest setup/source/logs/packets remain. Feature branch only. Notification inbox: [ADR-0065](docs/adr/0065-native-notification-inbox.md) proposes cached typed rendering of the existing self-scoped inbox and unchanged mark-all action. Reviewed product and six reproduction methods are applied (75a528c4/96601b3c); mechanical formatting is recorded. Focused web race13roots/72named0fail0skip, App/web vet, five overlay compiles, authcore snapshot, full native format/syntax/docs pass. First complete attempt f087 passed web199/863 but failed one NEW raw timestamp assertion in App; other five packages unrun, all five CIDs/privatePG cleaned. Independent5973 approved test-only HTML-attribute decoding repair9aac, applied with production/old assertions unchanged; fresh committed complete PG7/UI/measurement and actual acceptance remain pending. Producer/auth/retention and 31 accepted previous bodies remain protected.

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

- GUI preparation (ADR-0056/E5Q2): Q6 no-flag focused tests are source-only, without skips; flagged runs retain all requirements. Committed [profile runner](tools/gui-public-golden/managed_profiles.py) exports clean Git source and preserves17/28/45/strict cleanup; [79b current run](docs/reviews/gui-preferences-get-transport/repro-current-checkpoint.json) passed90/3packages,100GET+12API/strict cleanup. [Preparation plan](docs/execplans/gui-app-migration-a.md) completed its finite slices; formal S3A is BLOCKED-DEP on S0b/owner kit sync and exact ownership/capture applicability review. [Current preference/G1 proof](docs/reviews/gui-preferences-get-transport/README.md):22f actual90profile actions/89labels/3packages PASS (default17+preferences28+G1-45),100registeredGET/50pairs,12settingsAPI GETs; canonicalHTMLhard0, strict negatives/4-input custody refusals,formatter4/guards/PG2freshDBs/ownedcleanup passed. Historical691/794 failures retained; no old=current semantics/authprivacy/Live/full/device/default/adoption claim. [Campaign meter proof](docs/reviews/gui-campaign-meter/README.md):3bc actual65 once PASS/298JSON (24new+all41preferences),34strict hash rows,formatter0/ownedcleanup;126/18/7/raw38/private55/d722 exact. Exact fractionalSVG/scopedCSS and owning preferenceCSS inverse; full currentCSS served. No browser geometry/CSP/finance/authprivacy/full/device/UI/templ/adoption claim. Current G1 OS/FS registered GET transport813f PASS:45 once+package/222JSON,37pairs/74GET/31HTML+2anonymous302+4native404/7refusals; formatter0/source124+current20+18+7+PG6+raw38+private55 exact/ownedPG+CID cleanup. [Current checkpoint](docs/reviews/gui-original-get-walk/current-transport/checkpoint.json); current-current template/catalog only, no semantic original oracle/normalized baseline/full/privacy/Live/device/templ/adoption; later docsH not retested. G1 strict frozen-raw receiver fce7/R4 PASS:56 once+package/273JSON,31HTML hard0/6nonHTML/7mutations/10custody; actual38 wrapper API attempts, not process/browser census; formatter0/110+18+7+38+55 guards/owned cleanup; [receiver checkpoint](docs/reviews/gui-original-get-walk/receiver-checkpoint.json). No renderer parity/accepted normalized baseline/full/M0/privacy/Live/device/adoption. Existing raw historical-source/current-native oracle [a115/R6 proof](docs/reviews/gui-original-get-walk/README.md):54 once PASS+package/220events,37GET(31HTML200/2anonymous302/4native404),9mutations/6refusals,format0/108+102guards/owned PG+CID cleanup;20 historical75dc/native14 joins. No literal75dc binary/Normalize/full/M0/privacy/Live/device/adoption. R2–R5 failures retained; later docsH not retested. REST assertion closure remains separate:6 social API declarations, booking assertions, export query equality and
  existing donation REST assertions; [bounded plan](docs/reviews/native-go/rest-contract-plan.md). Finance service/HTML gaps remain separate. [Public GUI fixtures](docs/gui-public-golden-fixtures.md):[SOC-5 e13/prior proofs](docs/reviews/gui-public-original/community-style-checkpoint.json) retained. SOC-6 [bdeb scoped proof](docs/reviews/gui-public-original/community-pagination-checkpoint.json):34oncePASS(14card+20pagination)/160events,format0/29+18guards/ownedcleanup;72 source-expected only, no invocation census. SOC-7 [b540 proof](docs/reviews/gui-public-original/places-nonce-checkpoint.json):46oncePASS/210events,fmt0/53+18guards/ownedcleanup; shared Home/activity PRESENTATION only. SOC-8 [39ef proof](docs/reviews/gui-public-original/preferences-layout-checkpoint.json):41oncePASS/189events,fmt0/65+18guards/ownedcleanup; synthetic authenticated adult PRESENTATION only, two earlier failed runs retained. SOC-9 [fc7 proof](docs/reviews/gui-public-original/activity-form-nonce-checkpoint.json):44oncePASS/190events,fmt0/77+18guards/ownedcleanup;8 OS/FS+23binding+10DOM synthetic adult PRESENTATION, prior8ff42PASS/2FAIL retained. SOC-10 [44cc proof](docs/reviews/gui-public-original/activity-browse-nonce-checkpoint.json):37oncePASS/170events,fmt0/79+18/cleanup; synthetic adult presentation only. SOC-11 [7d85 proof](docs/reviews/gui-public-original/my-meetups-nonce-checkpoint.json):native25oncePASS/112events+VM8fake-only,fmt0/86+18/cleanup; no worker/cache/privacy approval. No recapture/default/SDK/backend filters/admission/browser style/CSP/cohort/privacy/full/adoption/device acceptance.
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
