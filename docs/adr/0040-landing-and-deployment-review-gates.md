# ADR-0040: Landing and deployment review gates

Date: 2026-10-05
Status: accepted (owner decision 2026-10-05)
Amends: [ADR-0032](0032-complete-native-go-backend.md) §Landing and release gates (landing paragraph).
Supersedes: AGENTS.md "require human review before landing" for the undeployed phase.

## Decision

While Social is undeployed, green and independently reviewed work may land on `main`
(agent-ops ADR-0014, direct landing in development). Green means, on the exact head landed:

1. `scripts/check-native.sh /absolute/path/to/go` passes.
2. `scripts/qualify-native.sh` passes every lane the change affects — all 21 lanes for an
   integration-branch landing — each lane with zero skips and PASS > 0.
3. `cd frontend && npm ci && npm test && npm run build` passes when frontend or embedded assets change.
4. `git diff --check` is empty; the fleet doc gate reports dead_links/stale_terms/retired_verbs/orphans 0.
5. Exact commands, head and counts are recorded in STATUS.md and WORKLOG.md. Tests the host
   cannot run are listed as not run, never as passed.

An independent reviewer (not the implementer) approves the final diff and confirms that new
tests fail without the fix. A dispatched hosted run ([ADR-0033](0033-manual-github-actions.md))
is optional evidence: never required, never claimed unless it ran on that head.

Human review of auth, erasure, privacy and safety code is a gate before first deployment, not
before landing. Scope: `services/authcore`; `internal/accounts` including login, erasure and
administrator bootstrap; `internal/safety`; media erasure; messaging authority; administrator
permissions; rate/action budgets. The record names the reviewer and the reviewed revision.
Landing never activates deployment, providers, minors, ingestion or paid infrastructure.

## Context / why

Social is not deployed; only the owner's machines run it. Since ADR-0033 CI runs only on
dispatch, so nothing defined "green" for a landing. On 2026-10-04 the owner authorized the Go
conversion landing (cbf8cdc, 319 files incl. authcore, accounts, erasure, safety) without
reading or reviewing its code; the record called that authorization the human review. It was
not. Why not keep human review before every landing: it would block all integration on one
reviewer while nothing is exposed to users; the risk it guards against starts at deployment.

## Consequences

- ADR-0032's claim that the 2026-10-04 approval satisfied the implementation review gate is
  withdrawn (its 2026-10-05 amendment); that human code review is pending.
- AGENTS.md, `docs/agent-testing.md` and `docs/SECURITY.md` point here for "green".
- Open deployment blockers: [RELEASE_READINESS](../RELEASE_READINESS.md) §Before first deployment.
- Revisit before first deployment, or if the owner restores automatic CI as a landing gate.
