# Native operator interface

`admin.New(db, catalog, social, safety, media)` binds the existing native domain services.
`admin.HTTP{Service: service, Auth: auth}.Register(mux)` mounts `/admin/` and the
Django-style `/admin/<app>/<model>/` model pages. The shorter `/admin/<app>.<model>/`
form is also accepted. Every read rechecks current database staff/active flags. HTML,
JSON metadata and result messages are private and `no-store`; credentials, blob keys
and encrypted-message bodies never appear in model summaries.

The 54 source-registered model summaries are bounded to 200 rows. Named actions accept
at most 200 distinct positive IDs, preserve per-record success/failure, and use domain
transactions for proposal publication, correction decisions, steward claims, edge
reversal, hours/closure/event report resets, appeal resolution and cover deletion.
Identity release/lifting and the administrative post visibility escape hatch retain
an audit transaction. Author-deleted posts cannot be republished through the escape
hatch. Audit, referral, one-account and ban ledgers cannot be hand-created/deleted.

`Service.Save(ctx, actor, model, id, fields)` receives a declared JSON field map; `id=0`
creates a row. Its typed allowlist covers taxonomy categories/types/relations, verified
civic partners, child venue classes/individual approvals, city Areas, calendar feeds,
booking links, campaigns, spend, cost anchors, in-kind contributions, civic outcomes,
and manual events. It validates required fields, lengths, choices, URLs, nonnegative
integer amounts, actual foreign keys, verified/active partner credits and taxonomy
cycles. Unknown fields fail. Server timestamps and staff approval identity are derived,
never posted. Every successful save records the edited field names in the hash chain.

Imported event facts retain source ownership. Holding/releasing an imported event uses
`ReviewEvent`, an explicit audited staff decision, with a nonempty review reason,
public venue, live source lifecycle and coherent canonical RO-EDU pack/confidence/license
metadata. Raw source identity/commerce/provenance cannot be changed through `Save`.

The safety console remains the native `/moderation/` service for reports, appeals,
formative concerns and human teen notes. Raw Django admin edits to account age/cohort,
consent, memberships, payment settlement, scanner verdicts and E2EE ciphertext are
intentionally rejected here; the corresponding native verification, guardian, voting,
payment, scanning and messaging services own those transitions. Unrestricted model
deletion remains outside this interface.

## Reviewed account permissions

[ADR-0035](../../../../docs/adr/0035-guarded-permissions-private-schema.md) records the
guarded native permission policy. In `/admin/accounts/user/` (also
`/admin/accounts.user/`), a current active staff superuser with `role=admin` receives
the permission form. An operator sees account summaries without this form; a role-only
moderator uses `/moderation/` and receives no access to the model console.

Select one account record ID and a complete permission level, enter a required review
reason (at most 2000 characters), then submit the same-origin CSRF-protected form:

| Level | Native role | Staff console | Superuser / permission management |
|---|---|---|---|
| User | `user` | no | no |
| Moderator | `moderator` | no | no |
| Operator | `user` | yes | no |
| Administrator | `admin` | yes | yes |

`ChangePermissions` reloads actor and target in its transaction. Adult managers and
newly privileged targets need the latest actual adult assurance to remain current,
matching verified adult age/cohort flags. A dedicated unverified/unassigned administrator
can manage permissions only with its native administrator-bootstrap audit provenance;
this grants no product participation. Grants to minors, inactive, pending or expired
accounts fail. Reductions can repair unsafe legacy permissions. Self-escalation fails.
Legacy Django groups and individual permissions are not native capability inputs.

Permission changes serialize and lock the involved accounts; removing an administrator
requires another currently eligible manager. Safety sanctions and GDPR erasure retain
their independent authority to restrict/delete any account, including the final admin.
Every effective change records only before/after permission fields and the review reason
in the governed audit, then revokes all target native sessions and API tokens in the same
transaction. Audit failure rolls everything back. Exact no-ops preserve credentials and
create no change audit. Targets must sign in again after a change; existing OAuth identity
links do not cache administrative capabilities. Permission fields grant neither private
thread membership nor cross-cohort messaging/parental authority.

Owned curated saves, event review and local operator actions lock/recheck current staff
authority in their transaction. Existing delegated domain services retain their own gates;
requests already authorized before a revocation may finish. Subsequent HTTP requests and
live deliveries reload credentials and current authority. Human auth/privacy review still
precedes landing this change.

Qualification uses the explicit `-admin-test-dsn` flag and synthetic isolated schemas.
Tests exercise every source summary query, fresh staff revocation, unknown-field and
identity/payment/scanner rejection, partner-public gates, taxonomy cycles, audited
identity release and native proposal/correction workflows. No live operator data is read.
