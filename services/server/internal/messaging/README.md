# Native secure messaging

`New(db, cursor)` implements the 14 existing messaging paths under `/api/` and
`/api/v1/`. Registration returns the owner's opaque backup only to that owner;
contact keys include the existing 32-hex SHA256 fingerprint and verification
state. Rotation invalidates old verification through the current fingerprint.
Explicit group creation remains a group with one invitee. Invitations, leaving,
group administrators, participant keys, disappearing timers and transparent
read-only CHILD guardian observers retain their domain behavior.

`Post` is a ciphertext relay. It has no decryptor, clear private key, passphrase
or message-content scanner. The provided key set must equal every active
participant, including the sender and any eligible guardian. Audit/notification
metadata never includes ciphertext, wrapped keys or reporter-disclosed content.
Report evidence is stored only in the established private safety report surface.

The native port closes stale-access gaps with fresh account, cohort, consent,
assurance, peer-block and guardian-basis checks. A block between active peers
denies access to their shared conversation until it is resolved. Public JWKs reject
private or unexpected fields; opaque backups reject clear private-key/passphrase
fields. Guardian discovery and read access require a currently eligible adult
and an active eligible CHILD ward. Review gates for these stricter checks:
[ADR-0040](../../../../docs/adr/0040-landing-and-deployment-review-gates.md) (independent review before landing; human review before first deployment).

`EnsureSchema` installs two per-user rate counters. Send admission commits before
recipient work, so malformed recipient attempts still consume the existing
60-per-minute budget. Start admission uses 20 per minute, with direct reuse before
the budget. Conversation pages cap at 100 and message windows at 50. Serialization
uses batched users/avatars, avoiding a query for every participant or sender.
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
invite acceptance, exact key sets, history cursors, blocks, report evidence,
retention, guardian withdrawal and real cross-replica WebSocket revocation.
