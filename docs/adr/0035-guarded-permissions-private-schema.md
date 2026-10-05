# ADR-0035 — Guarded administrator permissions and private API schemas

Date: 2026-10-04
Status: proposed; landing per ADR-0040; human auth/privacy review gates first deployment
Extends: [ADR-0032](0032-complete-native-go-backend.md)'s governed administration boundary.

## Decision

Expose account platform permissions as one reviewed service operation through the existing
native CSRF-protected administrator page. Accept only a target account ID, a complete
capability preset and a required bounded review reason. The presets are user
(`user,false,false`), moderator (`moderator,false,false`), operator (`user,true,false`)
and administrator (`admin,true,true`), in role/staff/superuser order. A moderator receives
moderation capabilities without acquiring the operator's model summaries.

Only a current active staff superuser with role admin may change permissions. Reload and
lock actor/target rows inside the mutation transaction; incoming actor flags are not proof.
Adult permission managers, privileged grant targets and peers counted for last-manager
protection must have matching verified adult flags and a current latest actual adult
assurance. Preserve the dedicated unknown/unassigned administrator-bootstrap exception
only with its audit provenance. Never grant age proof, cohort, parental consent, private
membership, provider/scanner authority or account activation through this operation.

Reject self-escalation; allow reductions of legacy unsafe privileges. Serialize permission
changes and protect the last eligible administrator, including concurrent demotions of
different accounts. Safety restrictions and GDPR erasure keep independent authority over
all accounts; availability must not obstruct those obligations. Granting/reducing permission
revokes every existing target native session and API token, atomically with the field update
and hash-chained audit. Exact no-ops neither revoke credentials nor manufacture audits.
Legacy Django group/individual permission rows are outside native authorization.

Document native private API request/response fields from actual Go DTOs and reviewed
handler projections. Validate schema references, field contracts and complete route
coverage against native registrations (378 operations, 316 paths, 145 component schemas).
Documentation contains contracts, never live
records, person examples, credential values or unpublished cohort data.

## Context / why

The offline Django administrator exposes broad raw model edits. Reproducing that surface
would bypass current verification, child consent, erasure, scanner, payment and E2EE domain
services. The native operator console already routes curated writes through governed
services, but lacked a permission-management transition. The API inventory documented
378 operations with field-level private DTO schemas left unspecified. Source-registration
validation corrects the earlier 330-path claim to 316 actual registered API paths.

A privilege grant must not elevate a previously compromised token/session, and a reduction
must not leave a stale administrative capability. Current credentials are database-backed;
HTTP and live delivery reload them and current actor state. Revoking target credentials on
every effective capability change makes fresh authentication necessary in both directions.

## Consequences

The account permission form is visible only to eligible permission managers. Operator
workflow and fixture qualification are described in the [admin guide](../../services/server/internal/admin/README.md).
Owned curated saves, event review and local operator mutations also recheck/lock staff
authority within their transaction. Already-authorized delegated domain requests retain
their existing transaction semantics; revocation does not retroactively cancel every
in-flight request. Safety sanctions/erasure can still remove the last administrator.

No unrestricted deletes, identity/consent/cohort CRUD, legacy permission-table edits,
deployment, scheduled jobs, real ingestion, provider/minor activation or production
verification are introduced. This sensitive source change needs human review before
landing. Exact tests and implementation evidence belong in STATUS.md and WORKLOG.md.

## 2026-10-05 — Console requires an administrator (owner decision, IDP-3)

Review finding IDP-3 showed that the operator preset (`user,true,false`) reached the whole
native model console: every model summary and row list, named actions including ban lifting,
identity-binding release and report-driven bans, curated saves and event hold/release. The
reference administrator gives a non-superuser staff account nothing without per-model
view/change permissions. The owner decided that the `/admin/` console is for administrators.

The operator preset no longer grants the model console. The whole console — model inventory,
row lists, named actions, curated saves and event review — requires an active staff
superuser. The incoming actor must carry all three flags, the account row is re-read and
must still hold them, and owned console mutations re-check them under the actor row lock
inside the mutation transaction. Service calls refuse anyone else as forbidden; the HTTP
front door answers them with the same 404 as an anonymous visitor. For any non-superuser
account this is at least as strict as the reference per-model permissions. Operators keep
their in-app staff powers outside the console; those checks are unchanged.

Legacy Django group/individual permission rows remain non-inputs: they neither grant nor
narrow console access. A finer operator tier inside the console needs its own reviewed ADR.
