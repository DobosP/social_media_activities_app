# ADR-0045 — Guardian authority: oversight across blocks, revocation at adulthood, ward exports

Date: 2026-10-05
Status: accepted (owner decisions 2026-10-05 via audit session G2); landing per ADR-0040; human safety review gates first deployment
Extends: [ADR-0036](0036-fresh-participation-authority.md).

## Decision

1. **Oversight survives a block (GO-07, reference parity).** A block between a guardian and their ward,
   in either direction, does not end the guardian's transparent observer reading of the ward's
   conversations. The relationship ends only through guardian or consent revocation or moderation.
   This prevents "block your parent" evasion. Guardian alerts (moderation notices, "I feel unsafe")
   stay suppressed by such a block, as before.
2. **Authority ends at adulthood (IDP-4, departs from the reference).** When age re-verification moves
   a ward to the adult cohort, every active guardian relationship where they are the ward and their
   active parental consents are revoked in the same transaction, with audit, and the former guardians'
   observer seats are pruned. Ward lookup, guardian erasure, ward export and acting on the ward's
   behalf also require the ward's current cohort to be a minor cohort, so a stale link can never act
   on an adult account.
3. **The guardian's ward export omits what can stand in for a report (IDP-5 and a follow-up decision,
   departs from the reference).** It leaves out the safety reports the child filed (detail, resolution
   and counts), the list of accounts the child blocked, and the child's own concern flags. The child's
   own export is unchanged.

## Consequences

The membership test that logged a "policy-review gap" now asserts the decided oversight behaviour in
both block directions. A former guardian can no longer erase, export, rename or act on behalf of an
adult account, and what a child reported or whom they blocked is not handed to the family.
Each adulthood revocation is audited as `guardian.revoked` with the former ward as actor, the guardian
as target and reason `ward_adult`; consents are revoked before links, the order a guardian's own
erasure uses. Observer seats leave through the existing cohort-change eviction, which records the
ward's own eviction as `messaging.participation_revoked` with a count but writes no per-seat
`messaging.guardian_observer_ended` row (unlike guardian revocation). Pending invitations addressed to
the new adult stay pending but can no longer be accepted, since acceptance requires a minor ward.
