# Project docs

Design, operations and decision docs for the Social Activities App.
**Start at the repo root: [`STATUS.md`](../STATUS.md)** (single source of current truth) and
[`CLAUDE.md`](../CLAUDE.md) (invariants + conventions). Index regenerated 2026-07-02.

## Current state & priorities

| Doc | What it covers |
|---|---|
| [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) | **The live gap list.** §0 "Already built — do NOT rebuild", then P0/P1 operational + legal work. Feeds `STATUS.md`. |
| [FEATURES_BUILT.md](FEATURES_BUILT.md) | **Built features + their invariant gates** — the behavioral-contract catalog (moved out of `CLAUDE.md` 2026-07-02). Check before building anything "new". |
| [ROADMAP.md](ROADMAP.md) | The original phased plan (D1–D10) + feature traceability. All deliverables shipped; kept for the map, not for status. |
| [archive/COMPLETENESS_GAPS_2026-06.md](archive/COMPLETENESS_GAPS_2026-06.md) | Gap tracker for the audited 2026-06 feature waves — open P0/P1/P2 items still live here. |

## Architecture & product design

| Doc | What it covers |
|---|---|
| [ARCHITECTURE.md](ARCHITECTURE.md) | D1-era system shape + the seams everything plugs into (see its do-not-rebuild note). |
| [ASYNC_TASKS.md](ASYNC_TASKS.md) | The Postgres `DeferredTask` queue contract + what may never be deferred (ADR-0003). |
| [DATABASE.md](DATABASE.md) | Postgres/PostGIS usage strategy (see its 2026-07-02 as-of note). |
| [MESSAGING.md](MESSAGING.md) | E2EE direct/group messaging — honest reference incl. guardian oversight (ADR-0006). |
| [MEDIA_FILTERING.md](MEDIA_FILTERING.md) | Media screening plan: fail-closed hash layer now, vetted vendor later (ADR-0004). |
| [FILE_STORAGE.md](FILE_STORAGE.md) | Object-storage design for media blobs. |
| [PUBLIC_GROUPS_DESIGN.md](PUBLIC_GROUPS_DESIGN.md) | Persistent cohort-pinned groups design. |

## Operations & deploy

| Doc | What it covers |
|---|---|
| [HOSTING_EU.md](HOSTING_EU.md) | **Deploy source of truth**: single Hetzner EU box + Hetzner Object Storage (ADR-0001); `render.yaml` = demo only. Provider procurement not yet final. |
| [RUNBOOK.md](RUNBOOK.md) | Operating the deployed app: envs, backups, incident response, sanction durations. |
| [SCALING.md](SCALING.md) | Scale-out levers in order (presigned media, PgBouncer, replicas, partitioning). |
| [RELEASE_READINESS.md](RELEASE_READINESS.md) | The "safe enough to launch" gate mapped to code. |
| [ROLLOUT_ACCOUNTABILITY_2026-06.md](ROLLOUT_ACCOUNTABILITY_2026-06.md) | Rollout accountability record for the 2026-06 waves. |
| [MULTI_AGENT_BUILD.md](MULTI_AGENT_BUILD.md) | *Superseded* by `AGENTS.md` (2026-06-24) — historical parallel-build pattern. |
| [agent-map.md](agent-map.md) · [agent-testing.md](agent-testing.md) | Agent orientation: app map + test commands. |

## Safety, security & compliance

| Doc | What it covers |
|---|---|
| [SAFETY.md](SAFETY.md) | **The authoritative child-safety invariants** + standing NO-GO-with-minors posture. |
| [SECURITY.md](SECURITY.md) | Supply-chain (pinning policy, ADR-0005) + app-security baseline. |
| [THREAT_MODEL.md](THREAT_MODEL.md) | STRIDE threat model. |
| [COMPLIANCE.md](COMPLIANCE.md) | EU/RO legal landscape (eIDAS/EUDI, GDPR+L190, DSA, CSAR). |
| [legal/](legal/) | **DRAFTS pending a DPO**: DPIA, ROPA (see its §5 gaps note), breach runbook, compliance checklist. |

## Data & integrations

| Doc | What it covers |
|---|---|
| [DATA_PROVIDERS.md](DATA_PROVIDERS.md) | The live place/event provider registry. |
| [DATA_AND_INTEGRATIONS.md](DATA_AND_INTEGRATIONS.md) | Source strategy + booking phasing (see its as-of note). |
| [ROEDU_INTEGRATION.md](ROEDU_INTEGRATION.md) | RO-EDU platform ingestion (places/events/app-packs), current working state. |

## Decisions & history

- [adr/](adr/) — **Architecture Decision Records** (`0000-template.md`): 0001 Hetzner hosting ·
  0002 cohort/connections policy · 0003 Postgres DeferredTask, no Celery · 0004 media screening ·
  0005 dependency pinning · 0006 E2EE over scanning. On conflict: `STATUS.md` > newest ADR > other docs.
- [archive/](archive/) — dated, superseded/completed records (2026-05 audits, hardening plan,
  Phase-2 plan, workboard, feature catalogs, 2026-06 changelog, gap tracker). Each carries a
  banner; immutable — do not update.

Status legend used across docs: ✅ done/in place · ▶️ recommended next · ⏳ later/scale · 🧊 backlog
