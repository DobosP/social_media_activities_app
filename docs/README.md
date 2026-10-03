# Project docs

Design, operations and decision docs for the Social Activities App.
**Start at the repo root: [`STATUS.md`](../STATUS.md)** (single source of current truth) and
[`AGENTS.md`](../AGENTS.md) (operating contract; hard invariants live in [SAFETY.md](SAFETY.md)).
Index regenerated 2026-09-05.

## Current state & priorities

| Doc | What it covers |
|---|---|
| [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md) | **The live gap list.** §0 "Already built — do NOT rebuild", then P0/P1 operational + legal work. Feeds `STATUS.md`. |
| [FEATURES_BUILT.md](FEATURES_BUILT.md) | **Built features + their invariant gates** — the behavioral-contract catalog (moved out of `CLAUDE.md` 2026-07-02). Check before building anything "new". |
| [ROADMAP.md](ROADMAP.md) | The original phased plan (D1–D10) + feature traceability. All deliverables shipped; kept for the map, not for status. |
| [archive/COMPLETENESS_GAPS_2026-06.md](archive/COMPLETENESS_GAPS_2026-06.md) | Gap tracker for the audited 2026-06 waves — immutable; treat an unticked box as a hypothesis to verify against HEAD, not a specification (see [STATUS.md](../STATUS.md) §Open work). |

## Architecture & product design

| Doc | What it covers |
|---|---|
| [reviews/go-foundation/README.md](reviews/go-foundation/README.md) | First Go serving-slice qualification and remaining rollout gates (ADR-0031). |
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
| [HOSTING_EU.md](HOSTING_EU.md) | **Deploy source of truth**: single Hetzner EU box + Hetzner Object Storage (ADR-0001); `render.yaml` = demo only. Provider procurement not yet final. Incl. the optional Go `agentapi` sidecar for AI-agent read traffic. |
| [RUNBOOK.md](RUNBOOK.md) | Operating the deployed app: envs, backups, incident response, sanction durations. |
| [SCALING.md](SCALING.md) | Scale-out levers in order (presigned media, PgBouncer, replicas, partitioning). |
| [RELEASE_READINESS.md](RELEASE_READINESS.md) | The "safe enough to launch" gate mapped to code. |
| [ROLLOUT_ACCOUNTABILITY_2026-06.md](ROLLOUT_ACCOUNTABILITY_2026-06.md) | Dated operator record (2026-06) for the #65 rollout flags `IDENTITY_UNIQUENESS_ENFORCED` and `PROGRESSION_AVATAR_PUBLIC` (both default False, `config/settings/base.py`) plus the EUDI prod settings; still present as of 2026-09-05 — verify before flipping. |
| [MULTI_AGENT_BUILD.md](MULTI_AGENT_BUILD.md) | *Superseded* by `AGENTS.md` (2026-06-24) — historical parallel-build pattern; immutable. |
| [agent-map.md](agent-map.md) · [agent-testing.md](agent-testing.md) | Agent orientation: app map + gate commands. Read order lives only in [`AGENTS.md`](../AGENTS.md). |

## Component & operator READMEs

| Doc | What it covers |
|---|---|
| [`../deploy/README.md`](../deploy/README.md) | Terraform/cloud-init runbook (apply gated on explicit owner go-ahead). |
| [`../db/README.md`](../db/README.md) | Seed-data operations. |
| [`../services/agentapi/README.md`](../services/agentapi/README.md) | Go sidecar config + privacy invariants. |
| [`../services/agentapi/landing.md`](../services/agentapi/landing.md) | Public text served at `/agent/v1/`. |
| [`../WORKLOG.md`](../WORKLOG.md) | Dated history (the overflow target for `STATUS.md`). |

## Safety, security & compliance

| Doc | What it covers |
|---|---|
| [SAFETY.md](SAFETY.md) | **The authoritative child-safety invariants** + standing NO-GO-with-minors posture. |
| [SECURITY.md](SECURITY.md) | Supply-chain (pinning policy, ADR-0005) + app-security baseline. |
| [THREAT_MODEL.md](THREAT_MODEL.md) | STRIDE threat model. |
| [COMPLIANCE.md](COMPLIANCE.md) | EU/RO legal landscape (eIDAS/EUDI, GDPR+L190, DSA, CSAR). |
| [legal/](legal/) | **DRAFTS pending a DPO**: [DPIA.md](legal/DPIA.md), [ROPA.md](legal/ROPA.md) (see its §5 gaps note), [BREACH_RUNBOOK.md](legal/BREACH_RUNBOOK.md), [COMPLIANCE_CHECKLIST.md](legal/COMPLIANCE_CHECKLIST.md). |

## Data & integrations

| Doc | What it covers |
|---|---|
| [DATA_PROVIDERS.md](DATA_PROVIDERS.md) | The live place/event provider registry. |
| [DATA_AND_INTEGRATIONS.md](DATA_AND_INTEGRATIONS.md) | Source strategy + booking phasing (see its as-of note). |
| [ROEDU_INTEGRATION.md](ROEDU_INTEGRATION.md) | RO-EDU platform ingestion (places/events/app-packs), current working state. |

## Decisions & history

- [adr/](adr/) — **Architecture Decision Records**; the number ledger (slug · status · date · next free number, and the duplicated `0009`) is [adr/README.md](adr/README.md). On conflict: `STATUS.md` > newest ADR > other docs.
- [archive/](archive/) — dated, superseded/completed records (2026-05 audits, hardening plan,
  Phase-2 plan, workboard, feature catalogs, 2026-06 changelog, gap tracker). Each carries a
  banner; immutable — do not update.

Status legend used across docs: ✅ done/in place · ▶️ recommended next · ⏳ later/scale · 🧊 backlog
