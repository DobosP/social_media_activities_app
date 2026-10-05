# ADR-0043 — Direct-only block veto, moderation chat eviction and direct re-invite

Date: 2026-10-05
Status: accepted (owner decision 2026-10-05 via audit session G2); landing per ADR-0040; human safety review gates first deployment
Extends: [ADR-0032](0032-complete-native-go-backend.md), [ADR-0036](0036-fresh-participation-authority.md).
Related: [ADR-0041](0041-report-eligibility-predicate.md).

## Decision

Messaging matches the reference (Django `can_view` / `post_message`) for group chats:

- **A block stops only direct chats.** In a direct conversation a block in either direction with the
  other active participant still denies read, send and live access. In a group conversation blocks are
  not consulted for reading or sending; clients may hide a blocked sender's messages. Adding a member
  still requires the pair to be eligible and unblocked.
- **Admins can always remove a member.** The admin check for participant changes is block-exempt; an
  admin must still be an active, eligible participant of the conversation's cohort.
- **Sending validates the sender only.** The required recipient-key set is every active participant
  whose account is active — the same predicate as the key roster the client wraps for — and peers are
  not re-validated per message. Send cost no longer grows with group size.
- **A suspended or banned user is removed from their chats** in the moderation transaction
  (suspend, timed ban and ban on an account): participant rows become `removed` and guardian observer
  seats are pruned. The removal is permanent: lifting a suspension or overturning it on appeal does not
  restore memberships. Group chats need a fresh invitation from an admin.
- **Direct re-invite (owner approved):** starting a direct chat with someone whose earlier direct chat
  was left or removed sends the peer a new invitation they must accept, after the normal
  pair/block/eligibility checks. A starter who left or was removed re-enters only when the peer is not
  active either; while the peer is active, the peer decides (no self-reactivation past someone's
  removal decision).
- **Block budget:** blocking and unblocking share a 30-per-hour budget, checked before the target is
  resolved.

This narrows docs/SAFETY.md rule 2 ("blocking is honoured both ways") to direct conversations by owner
decision; cohort walls, guardian read-only observation and the direct-chat veto are unchanged.

## Consequences

One member's block can no longer shut a whole group (GO-PRIV-02), and one suspended, lapsed or ineligible
member can no longer stop everyone else from sending (GO-PRIV-03). Accepted side effects: a member whose
consent or assurance lapsed still receives wrapped keys but cannot read until eligible again (reference
behaviour); a suspended sole group admin leaves the group without an admin; while a direct-chat blocker's
account is deactivated, the other side can post messages wrapped only for themselves. The messaging README describes the
group semantics; this ADR is the human privacy/safety sign-off that README asked for.
