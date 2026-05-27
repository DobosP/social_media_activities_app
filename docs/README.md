# Project docs

Orchestration and design docs for the Social Activities App. Start with the roadmap.

| Doc | What it covers |
|---|---|
| [ROADMAP.md](ROADMAP.md) | **Start here.** Every feature from the vision, slotted into deliverables D1–D9, with a dependency graph, cross-cutting "integrated steps", and a feature-traceability matrix. |
| [ARCHITECTURE.md](ARCHITECTURE.md) | System shape today (D1) and the seams every future deliverable plugs into. |
| [COMPLIANCE.md](COMPLIANCE.md) | EU/Romania legal landscape (eIDAS/EUDI, GDPR + Law 190, Online Age of Majority law, DSA, CSAR) and a compliance checklist mapped to deliverables. |
| [SAFETY.md](SAFETY.md) | Child-safety-by-design: threat model, invariants, and controls per deliverable. |
| [SECURITY.md](SECURITY.md) | Engineering & supply-chain security: the dependency-pinning/upgrade policy and the app-security baseline. |
| [DATA_AND_INTEGRATIONS.md](DATA_AND_INTEGRATIONS.md) | Place-data sources (OSM / Overture / Google) and the booking-integration strategy. |
| [MULTI_AGENT_BUILD.md](MULTI_AGENT_BUILD.md) | How to build with several Claude Code agents in parallel without conflicts (ownership, branches/worktrees, contracts, cross-branch sync, how many sessions + what to tell them). |
| [WORKBOARD.md](WORKBOARD.md) | **Live registry** of which session/branch owns which track — claim here before coding. |

Status legend used across docs: ✅ Done · ▶️ Next · ⏳ Planned · 🧊 Backlog

**Current state:** D1 (foundation + Romania place-data pipeline) is shipped; see the repo
[`README.md`](../README.md). D2 (identity, accounts & consent) is next.
