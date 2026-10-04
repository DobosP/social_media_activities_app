# Status — social_media_activities_app

Last verified: 2026-10-04

- **GitHub Actions (Last verified: 2026-10-04):** owner-requested on-demand policy,
  [ADR-0033](docs/adr/0033-manual-github-actions.md). Workflows use `workflow_dispatch`; automatic
  push/PR/label/schedule runs are removed. Existing jobs, inputs, and safety gates
  remain. Workflow YAML, manual inputs, job dependencies, and permission preservation
  were checked; this configuration edit does not refresh application test results.

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
  The default Docker/Compose/systemd/Render/cloud-init launch paths invoke Go. Shared authentication
  is a hash-verified portable copy; Django remains offline contract/reference tooling.
- The owner-approved Go conversion is merged and pushed to `origin/main` on 2026-10-04 (base conversion; completion candidate under review).
  Social deployment/provider/minor launch gates remain separate from source landing.
## Safety and operating gates

- A RO-EDU venue remains child-venue **UNKNOWN** until staff approve that exact
  place. Low-confidence and non-live lifecycle rows remain nonpublic.
- Canonical ingestion never copies descriptions, people, private/internal
  evidence, internal paths, or raw provenance. License/access metadata survives.
- Cohort, guardianship, block, minor-contact, moderation, consent, and
  private-thread visibility gates are unchanged by the V2 integration.
- Landing source does not run ingestion, enable scheduled sync, deploy, apply
  Terraform, or authorize minors. API keys and opt-in settings remain required.
- Launch is blocked on the GDPR/DPIA/DPO/parental-consent stack and production
  operations. Never apply paid infrastructure without owner authorization.

## Open work

- Reused sessions are resolving cross-review findings in profile authority, media/group controls and
  inherited fixed-window budget/erasure lock ordering; affected qualification is in progress.
- Profile review fix reloads the viewer after rate admission; source consent/minimal-card policy
  stays intact. Local fix/go-review-profile qualification precedes coordinator caller/integration review.

- ADR-0035 review candidate adds guarded administrator permissions and complete private API field
  contracts (380 operations/318 paths/166 schemas); human auth/privacy review precedes landing.

- All three completion lanes are integrated and locally qualified on `feat/go-migration-finish`.
  Human auth/privacy/safety review precedes landing; native hosted Actions is manually disabled.

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

- Cross-review fixes: coordinator account20/safety20 fixtures pass; inherited erasure deadlock
  reproduces40P01 under old overlay, passes after fix. Profile social5/web3 targeted race tests pass;
  group HTML/SPA/domain cohort-policy regression passes. Combined follow-up qualification pending.
- Prior completion iteration passed280 native tests/19 lanes, zero skips, plus race/vet/auth/audits.
  Its Go-only image453f7048 and HTTP/schema380/318/166 receipt is historical after these fixes.
  Exact receipts: `docs/reviews/native-go/completion-qualification.md` and WORKLOG.

## Standard verification
Native race/vet + shared-source hashes; database/codec contracts require explicit disposable fixtures
through `scripts/qualify-native.sh` (`docs/agent-testing.md`). Whitespace/doc gates are required.
Native CI: `.github/workflows/native.yml`; offline reference/frontend: `ci.yml`; operator: `docs/ROEDU_INTEGRATION.md`.

## Doc map

| Doc | Job |
|---|---|
| `AGENTS.md` | Operating contract: read first, commands, safety, docs discipline. |
| `docs/README.md` · `docs/agent-map.md` · `docs/agent-testing.md` | Full index · entry points and routes · gates. |
| `docs/PRODUCTION_READINESS.md` · `docs/ROEDU_INTEGRATION.md` · `docs/SAFETY.md` | Live gap list · RO-EDU operator contract · child-safety invariants. |
| `docs/adr/` · `WORKLOG.md` | Decisions · dated history. |
| vault `projects/social-media-activities-app.md` | Fleet role, status, next. |
