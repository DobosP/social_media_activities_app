# Status — social_media_activities_app

Last verified: 2026-07-16

This is the repo's single source of current truth. On conflict:
`STATUS.md` > newest ADR in `docs/adr/` > other docs. Detailed build history
stays in git, ADRs, and `docs/FEATURES_BUILT.md`.

## What this is

A children-first, in-person local-activities social app: no ads, deterministic
discovery rather than engagement ML, Cluj-Napoca first, EU residency required,
and donations only. `docs/SAFETY.md` owns the safety invariants.

## Current main

- **RO-EDU canonical places/events (ADR-0023/0024):** the only canonical product
  accepted is `roedu:social_media_activities_app:events_places:v1`, schema 1.
  Pages require one immutable promoted release/snapshot identity, coherent
  completeness, safe bounded pagination, exact facts-only fields, policy schema
  4/ruleset 6/hash
  `07f27d3c9a5e5898ba7cfac686c645713114dd9c13d72ecc054570d368daf58d`,
  and capture/acquisition schema 3. Unknown fields, prose/person data, internal
  evidence/paths, unsafe URLs, policy drift, malformed relationships, duplicates,
  and page drift fail closed.
- **Lifecycle and reconciliation:** source lifecycle/confidence/category,
  recurrence/timezone/price/availability, source timestamps, venue identity,
  and pack/release/snapshot identity are retained. Cancelled, postponed, removed,
  moved-online, tombstoned, low-confidence, or unsafe-venue events stay out of
  public discovery. Only an unbounded clean full snapshot can reconcile absence,
  atomically within its exact pack/city scope; partial/delta/legacy reads cannot.
- **Plural sentiment and moderation (ADR-0029):** fixed appreciation facets,
  adult-only dissent, private conduct concern, minor-protective thresholds,
  anti-pile-on/coordination sensors, batched public summaries, and audited human
  moderation are implemented for activity and group threads. Counts and
  engagement ranking are not exposed.
- **Private-thread media (ADR-0026):** canonical AVIF/WebP image processing and
  adult-only, cohort-gated private-thread video are implemented with fail-closed
  scanning, sandboxed transcoding, signed serving, retention/erasure coverage,
  and no public/discovery short-video surface.
- **Identity surfaces (ADR-0027/0028):** non-collectible signature-avatar styles,
  uniqueness enforcement, tiered profile visibility, block/cohort vetoes, and
  query-bounded hover cards are live. Minor pairs remain clamped.
- **Public/agent access (ADR-0025):** anonymous event/place APIs, gate-filtered
  snapshots with safe V2 source facts, event price/availability JSON-LD, the
  no-DB Go sidecar, and crawler contracts are implemented. Public activity export
  remains the hard-coded ADULT + explicit-listing subset.
- **Core product/runtime:** D1–D10 and the audited feature waves, phased
  React/Preact UI behind kill switches, API/CSP/header/readiness hardening,
  bounded ASGI/database/cache behavior, EU-hosting templates, and deferred jobs
  are present. The production Terraform has never been applied.

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
- The separate E2EE-DM reaction picker still needs cosmetic adaptation to the
  sentiment facet slugs. Operational gaps remain in
  `docs/PRODUCTION_READINESS.md`.

## Verification

- Fresh 2026-07-16 gates: Ruff 0.15.21 check/format and migration drift passed;
  the focused RO-EDU/lifecycle/public-projection suite passed 178 tests plus 27
  subtests; the full isolated PostGIS suite passed 2,672 tests with 30 skips and
  27 subtests; the producer→server→both-real-clients loopback passed 84 tests.
- No real network ingestion, deploy, or child-facing data mutation is part of
  these gates; consumer fixtures and the loopback serving projection are used.

## Standard verification

```bash
docker compose -p socialfix -f docker-compose.local.yml exec -T web \
  sh -lc 'python -m pytest -q'
git diff --check
```

See `CLAUDE.md` for the complete CI matrix and `docs/ROEDU_INTEGRATION.md`
for the operator contract.

## Agent notes

- Human review is required for privacy, moderation, child-safety, and auth changes.
- Never read or print secret values; update STATUS + ADRs with contract changes.
- Push/merge only when the owner explicitly asks and the required gates are green.
