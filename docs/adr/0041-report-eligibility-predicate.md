# ADR-0041 — Report eligibility is its own predicate

Date: 2026-10-05
Status: accepted (owner decisions 2026-10-05 via audit session G2); landing per ADR-0040; human safety review gates first deployment
Extends: [ADR-0032](0032-complete-native-go-backend.md). Related: [ADR-0043](0043-direct-only-block-veto.md).

## Decision

Reporting never reuses the block-filtered read gates. A block, a hidden activity or a lapsed thread
participation must not take away the DSA Art. 16 notice channel from the person who needs it most.
For non-staff reporters whose current account is active and in an assigned cohort:

- **Activity:** the activity is in the reporter's cohort. Blocks and `is_hidden` are ignored
  (reference `social.can_see_activity`).
- **Thread post:** the thread owner (activity or group) is in the reporter's cohort and the reporter
  owns it or holds a membership row that could have seen it (activity `member`/`removed`, any group
  row). A `requested`-only row, a never-member and another cohort stay refused. Accepted edge: a
  withdrawn or declined join request also leaves a `removed` row and can report a post by id.
- **"I feel unsafe":** a current non-owner, non-guardian member of a not-hidden activity in their cohort,
  checked inside the reporting transaction with the activity row locked, before the budget is spent.
  A block with the owner, in either direction, does not remove the button: a blocked member gets a
  minimal safe-exit page (title, unsafe button, detailed report link, leave) and can still leave.
  Leaving is self-withdrawal and no longer depends on the block-aware read gate (web and API; the
  reference's API leave was block-aware, its web leave was not).
- **E2EE message:** the reporter's account is active and they are an active participant of that
  conversation; the message belongs to it. Peer blocks are not consulted (reference
  `is_active_participant`).
- **User:** the target is active, pair-visible to the reporter and not blocked either way. The
  reference shows any account's name on `/report/?type=user&id=N`; that is a reference bug, not a
  parity target (audit F4).

Eligibility is wider than read access, so labels are not: an activity title the read gate would hide
(owner block either way, moderation-hidden) shows as "this activity", and an author or user across a
block, or with no display name, shows as "A member". Non-staff labels never show a username. Every
non-staff refusal is an indistinguishable not-found. Eligibility is checked before the report budget is
spent. User-target lookups on the report page are metered like profile cards (240 per hour, refused
lookups included; owner decision 2026-10-05), so names cannot be walked by sequential id.

## Consequences

The block-then-report regressions found by the 2026-10-05 audit (GO-PRIV-01) are closed: reporting a
harasser's activity, thread posts or DMs, and a child's unsafe button, survive a block in either
direction. PostgreSQL tests pin both block directions, hidden activities, reporting after leaving,
cross-cohort and never-member refusals, user-target visibility, generic labels, the safe-exit page, the
page budget and budget ordering. Residuals: a blocked member can tell "the owner blocked me" (safe-exit
page) from "the activity is hidden" (not-found); an unblocked member with lapsed consent still sees no
unsafe button on the normal page (pre-existing).
