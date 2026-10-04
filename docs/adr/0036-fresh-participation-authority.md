# ADR-0036 — fresh participation authority

Date: 2026-10-04
Status: proposed; locally qualified, awaiting human review before landing

## Context

Shared rate admission releases domain preflight locks, commits a reservation separately
and repeats the domain transaction. The request actor remains a snapshot. The existing
participation gate checked current assurance/consent, but trusted that snapshot's activity,
identity, cohort and capabilities. Withdrawal between transactions could therefore leave
the second transaction using stale authority.

## Decision

The common native participation gate requires an authoritative account row matching the
captured age band, cohort, role, staff and superuser capabilities, with current active and
verified flags. A changed snapshot is denied instead of moving the request into a different
cohort. The same query retains the existing assurance-expiry contract; the existing current
parental-consent query remains required for under-16 participation.

No identity, age, consent or capabilities are granted by this gate. Existing privacy withdrawal
paths retain their separate policy so expired assurance does not block lawful withdrawal.
The existing legacy-assurance adoption behavior remains unchanged. Authentication/private
delivery adapters retain their own current-credential and domain gates.

## Qualification and limits

Two PostgreSQL regression tests pass under the race detector in the Python-free release
codec image with an isolated synthetic database: seven independently changed account fields
reject the captured actor; expired assurance and revoked child consent reject participation.
Full combined native qualification follows the completion-lane integration.

This checks authority at the participation query's transaction snapshot. It does not promise
that an already-admitted operation is canceled after every subsequent concurrent change;
the owning domain still controls transaction locks and final delivery authorization.
Repository AGENTS.md requires human review of the concrete authentication/privacy/safety
changes before source landing. No production or product activation is implied.
