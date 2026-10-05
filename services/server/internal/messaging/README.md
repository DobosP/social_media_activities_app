# Native secure messaging

`New(db, cursor)` implements the 14 existing messaging paths under `/api/` and
`/api/v1/`. Registration returns the owner's opaque backup only to that owner;
contact keys include the existing 32-hex SHA256 fingerprint and verification
state. Rotation invalidates old verification through the current fingerprint.
Explicit group creation remains a group with one invitee. Invitations, leaving,
group administrators, participant keys, disappearing timers and transparent
read-only CHILD guardian observers retain their domain behavior.

`Post` is a ciphertext relay. It has no decryptor, clear private key, passphrase
or message-content scanner. Only the sender is validated (fresh active account,
conversation cohort, participation, not a guardian), as in the reference. The
provided key set must equal every active participant whose account is active,
including the sender and any guardian observer; `ParticipantKeys` serves the same
roster, and both are bounded without per-member queries. A peer whose consent or
assurance lapsed is neither validated nor excluded, so one stale member never stops
the others from sending. Audit/notification metadata never includes ciphertext,
wrapped keys or reporter-disclosed content. Report evidence is stored only in the
established private safety report surface.

The native port closes stale-access gaps with fresh account, cohort, consent,
assurance and guardian-basis checks for the viewer. Blocks follow the reference:
a block in either direction between the viewer and an active, non-guardian peer
denies a DIRECT chat until it is resolved. Group chats ignore blocks for reading,
sending and live access; clients may hide a blocked sender's messages. Adding a
group member still requires the admin/target pair to be unblocked and eligible,
but an active admin can always remove a member regardless of blocks. Guardian
observer reading is unchanged by ward/guardian blocks. Public JWKs reject private
or unexpected fields; opaque backups reject clear private-key/passphrase fields.
Guardian discovery and read access require a currently eligible adult and an
active eligible CHILD ward. Decision: [ADR-0043](../../../../docs/adr/0043-direct-only-block-veto.md);
review gates: [ADR-0040](../../../../docs/adr/0040-landing-and-deployment-review-gates.md)
(independent review before landing; human review before first deployment).

A moderation suspend, timed ban or ban calls `RemoveUser` (which prunes orphaned
guardian observers) in the same transaction as the sanction, so the account leaves
every conversation; safety refuses the sanction if messaging is not wired. Lifting
the sanction never restores rows. Instead, `Start` on an existing direct pair
re-invites a peer who left or was removed (the peer becomes invited and must
accept), only after `pair()` re-checks cohort, participation and blocks. A starter
who left or was removed re-enters only when the peer is not active either; while the
peer is active, the peer decides. Group members return only by an admin invitation.
Rows that change are charged to the start budget and audited as
`messaging.direct_reinvited`.

`EnsureSchema` installs two per-user rate counters. Send admission commits before
recipient work, so malformed recipient attempts still consume the existing
60-per-minute budget. Start admission uses 20 per minute; plain direct reuse comes
before the budget and is free, a direct re-invite is charged. Conversation pages
cap at 100 and message windows at 50. Serialization uses batched users/avatars,
avoiding a query for every participant or sender.
Native request and socket envelopes are bounded to 2 MiB; ciphertext is 64 KiB,
wrappers 4 KiB, JWKs 8 KiB and active recipients at most 256.

Web controllers can call `Start`, `Conversation`, `Conversations`, `Transition`,
`AddParticipant`, `RemoveParticipant`, `SetDisappearing`, `AddGuardian`,
`ParticipantKeys`, `Messages`, `Message`, `RegisterKey`, `OwnKey`, `ContactKey`,
`VerifyKey` and `Report`. Account/guardian revocation must call `RemoveUser` and
`PruneObservers` in its domain transaction. Formal account erasure must also
delete authored `messaging_messagekey`/`messaging_message` rows and memberships,
following the existing account erasure graph. `PurgeExpired(ctx, limit)` reclaims
ciphertext and wrapped keys using conversation timers and the configured global
retention backstop (default zero).

`LiveAdapter()` plugs into `chat.NewServer`. Message transactions publish only
conversation/message IDs through the schema-hashed PostgreSQL channel. Each
socket re-authenticates its session/token and domain permissions before inbound
work, before every delivery, and during an idle recheck. REST history only returns
rows with a wrapped key addressed to the requester; a new member receives no old
undecryptable history. Socket responses contain the complete already-wrapped key
set expected by the existing browser client.

Tests use an explicit disposable `-messaging-test-dsn`, clone only their own
`messaging_native_test_*` schema and never read environment credentials. The
integration suite exercises registry/backup/rotation, direct reuse, group shape,
invite acceptance, exact key sets, history cursors, direct-only blocks, group
blocks, admin removal under a block, moderation eviction and direct re-invite,
membership-independent send query counts, report evidence, retention, guardian
withdrawal and real cross-replica WebSocket revocation.
