# ADR-0046 — Membership rows and live logistics are visible to co-members only

Date: 2026-10-05
Status: accepted (owner decisions 2026-10-05 via audit session G2); landing per ADR-0040; human privacy review gates first deployment
Extends: [ADR-0032](0032-complete-native-go-backend.md).

## Decision

The membership list and detail APIs return only the caller's own memberships and the memberships of
activities where the caller is a current non-guardian member (co-organizers included) or the owner.
A block in either direction between the caller and a row's member also hides that row; the caller's own
rows always stay visible. Every other row in the cohort, including its live logistics (arrived, in
transit, departing), is invisible; detail returns the same not-found as any other invisible row, and the
list count matches the visible rows. Existing cohort, hidden and owner-block gates still apply.

This departs from the reference `MembershipViewSet`, which lists every membership of every visible
activity in the caller's cohort. The reference behaviour is recorded as a privacy defect, not a parity
target.

## Consequences

A child account can no longer watch other children's arrival and departure signals for meetups it is
not part of, and a blocked co-member's logistics stay hidden, as the web roster already does. Organizers
keep seeing and deciding join requests; requesters keep seeing their own row. A vote on a requester in a
block with the voter still applies, but its response row is then not-found.
