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

## Baseline regression receipt

The broader baseline passed175 PostgreSQL/codec race contracts with zero skips.
Saved-search jobs and guardian capability reads now load the actor capability fields
needed by the equality check. The catalog fixture also returns the same role it inserts.
The combined shared-budget/configuration/admin candidate still requires qualification.


## Profile disclosure follow-up (2026-10-04 review candidate)

Profile-card rate admission commits separately from its read projection. The profile
service now reloads the entire viewer row immediately after admission and replaces
captured request flags before cohort/tier resolution or private field selection. Current
inactive/unassigned/cross-cohort/self/mutual-block vetoes remain indistinguishable404s;
current target state and minor interest/photo clamps retain ADR-0028's field matrix.

Profile visibility remains separate from participation: a current assigned same-cohort
viewer with withdrawn verification or expired/revoked consent can retain a minimal card
or live shared context, while connecting is independently refused by current participation.
This does not authorize messaging, create memberships, or grant identity/parental consent.
The corresponding HTTP and HTML regressions exercise a real shared-admission trigger,
not a replacement rate helper; no nested pool acquisition is introduced.

The presentation helper supplies authorized card aliases and target IDs for report/block
forms. A connected-adult full-page photo uses the existing native media metadata endpoint,
which checks current viewer/target/block/scanner authority and signs a viewer-scoped URL.
Hover cards and minor/stranger cards retain generated avatars only. The coordinator owns
the narrow generic-person view caller; its exact caller is separately qualified in a
scratch Go overlay before integration.

Credential revocation rejects subsequent authenticated requests and live delivery.
This profile service receives an already authenticated actor, not the raw credential;
reloading current visibility does not claim to retroactively cancel every admitted read
or every concurrent change after its authority query. Human auth/privacy/safety review
still precedes landing. Exact fixture results are recorded in WORKLOG/STATUS.
