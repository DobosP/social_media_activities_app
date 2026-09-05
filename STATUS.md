# Status — social_media_activities_app

Last verified: 2026-09-05

This is the repo's single source of current truth. On conflict: `STATUS.md` > newest ADR in
`docs/adr/` > other docs. History: `WORKLOG.md` (dated, append-only), `docs/adr/`, git.

## What this is

A children-first, in-person local-activities social app: no ads, deterministic
discovery rather than engagement ML, Cluj-Napoca first, EU residency required,
and donations only. `docs/SAFETY.md` owns the safety invariants.

## Current main

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
  switches, API/CSP/header/readiness hardening, bounded ASGI/database/cache behavior, EU-hosting templates,
  and deferred jobs are present. The production Terraform has never been applied.
- **One nightly job, one RO-EDU credential (2026-08-22).** `sync_roedu` forwards `ROEDU_API_KEY` from the
  environment to the events lane (`apps/ingestion/management/commands/sync_roedu.py:78`);
  `sync_roedu_events` resolves `--api-key` or `ROEDU_API_KEY` and raises `CommandError` when neither is set
  (`apps/events/management/commands/sync_roedu_events.py:277-279`);
  `apps/ingestion/sources/ro_scraper.py:131` has no dev-key fallback. Detail: `WORKLOG.md` §2026-08-22.
- **A refused RO-EDU product is loud (ADR-0030, 2026-08-18).** `RoeduClient.iter_required` raises
  `RoeduProductUnavailable` with the page note; `ingest_places --source=roedu` and `sync_roedu_events`
  exit non-zero; the scheduled `sync_roedu` job logs/reports it and still runs `resolve_place_covers`
  so the shared compliance tick completes. Plain `iter` keeps core semantics. Detail: `WORKLOG.md` §2026-08-18.
- **DSA Art.17 redress + provenance (2026-08-09/10, owner-ratified 2026-08-12).** `Post.is_author_deleted`
  records the author's own withdrawal; `safety.targets_with_unlifted_remove` is the single implementation of
  "removal still in force"; the Art.16/17 record and the GDPR Art.20 export query each scope separately,
  newest-first; `PostAdmin.is_hidden` stays an operator escape hatch without provenance
  (`apps/social/admin.py`). Detail: `WORKLOG.md` §2026-08-09, §2026-08-10.
- **Canonical `/v1` client (2026-07-26, romania_scraper ADR-0069).** Transport and pagination come from
  the generated, stamped `apps/ingestion/sources/_roedu_client_core.py` (repeated-cursor guard, `pages()`);
  the publication gate stays local. The file is excluded from `ruff format` only — `ruff check` still lints
  it — and hand-edits fail `apps/ingestion/tests/test_roedu_client_vendored.py`. Detail: `WORKLOG.md` §2026-07-26.

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

- Build/promote a fresh immutable producer/server V2 release before real sync;
  the serving repo's producer dependency must be intentionally bumped first.
- Complete held-event review UX, curated cultural child-venue policy, localized
  taxonomy/cinema mapping, and production alerting/shared-state operations.
- Operational gaps remain in `docs/PRODUCTION_READINESS.md`. Treat an unticked box
  in `docs/archive/COMPLETENESS_GAPS_2026-06.md` as a hypothesis to verify against
  HEAD, not a specification — two backlog surveys turned already-shipped entries
  back into planned work.
- Hosting provider and box size are RECOMMENDED-NOT-CONFIRMED (ADR-0001 §To revisit; `deploy/README.md` banner,
  owner note 2026-07-02): reconcile by ADR before procurement. Never `terraform apply` without owner go-ahead.

## Verification record (newest first)

- 2026-09-05 (docs refresh): `python3 ~/work/agent-ops/scripts/check_docs.py .` → `files=37 dead_links=0 stale_terms=0 retired_verbs=0 orphans=0` (exit 0);
  `python3 ~/work/agent-ops/scripts/check_project_contexts.py --repo "$PWD"` → CLAUDE pointer yes, agent-ops ADR-0025 stanza yes, AGENTS/CLAUDE 80/3 lines,
  status doc 120 lines, notes ok; `git diff --check` clean. Container tests not run (the `socialfix` compose project is not up on this host): no pass count claimed.
- 2026-08-22 (credential fix, recorded in its merge): 142 passed across the touched lanes; full suite 2773
  passed, 15 failed — all in the chat and messaging `test_consumer.py` websocket tests, failing identically
  on pristine main in isolation; both ruff commands and `makemigrations --check` clean.
- 2026-07-16: Ruff 0.15.21 check/format and migration drift passed; focused RO-EDU suite 178 tests
  + 27 subtests; full isolated PostGIS suite 2,672 passed / 30 skipped; producer→server→clients
  loopback 84 tests; no real network ingestion, deploy, or child-facing data mutation in any gate.

## Standard verification

```bash
docker compose -p socialfix -f docker-compose.local.yml exec -T web \
  sh -lc 'python -m pytest -q'
git diff --check
```

Full CI matrix: `.github/workflows/ci.yml` (frontend, lint-test incl. `check --deploy`, docker-build +
Trivy, audit = pip-audit + bandit). Gate commands with expected output: `docs/agent-testing.md`. Operator
contract: `docs/ROEDU_INTEGRATION.md`.

## Doc map

| Doc | Job |
|---|---|
| `AGENTS.md` | Operating contract: read first, commands, safety, docs discipline. |
| `docs/README.md` · `docs/agent-map.md` · `docs/agent-testing.md` | Full index · entry points and routes · gates. |
| `docs/PRODUCTION_READINESS.md` · `docs/ROEDU_INTEGRATION.md` · `docs/SAFETY.md` | Live gap list · RO-EDU operator contract · child-safety invariants. |
| `docs/adr/` · `WORKLOG.md` | Decisions · dated history. |
| vault `projects/social-media-activities-app.md` | Fleet role, status, next. |
