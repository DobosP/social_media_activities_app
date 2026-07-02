# ADR-0002: Connections cohort policy — all cohorts by default, each within its own cohort

Date: 2026-05-30
Status: accepted

## Decision
`CONNECTIONS_ALLOWED_COHORTS` defaults to ALL cohorts (adult, teen, child), each connecting ONLY
within its own cohort; UNASSIGNED is never allowed (discarded unconditionally in code). The earlier
"CHILD can never be enabled even by misconfig" hard-wall is deliberately removed (`78e5a48`,
2026-05-30). The groups self-creation CHILD/TEEN hard-wall (`GROUPS_USER_CREATION_COHORTS`,
discarded unconditionally in `create_group`) STAYS — it is independent and unaffected.

## Context / why
The connections hard-wall never carried the adult↔minor guarantee: `can_connect` requires the SAME
cohort plus a shared cohort-pinned activity, so cross-age connection is structurally impossible
regardless of the setting. The wall only stopped children connecting with other children. Enabling
all cohorts is safe ("no matter age") because children additionally inherit the participation/
parental-consent gate and guardian-observable messaging on any resulting chat.
This decision previously existed ONLY in git history — docs that cited the removed wall
(CLAUDE.md, PUBLIC_GROUPS_DESIGN.md, `apps/connections/services.py` docstring,
`config/settings/base.py` comment) kept resurrecting it as precedent. This ADR is the canonical
record ending that contradiction class; all four citations were rewritten 2026-07-02.

## Consequences
- Never cite `CONNECTIONS_ALLOWED_COHORTS` as a "hard-wall pattern" precedent; the groups wall
  stands on its own.
- Child-safety review focus shifts to the same-cohort gate + consent + guardian observability,
  not the allowed-cohorts list.
- Revisit only if minor onboarding is enabled in prod (trust anchor + DPIA) — any change there
  needs a superseding ADR.
