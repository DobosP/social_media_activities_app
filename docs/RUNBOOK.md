# Operations and incident-response runbook

Verified against native Go on 2026-10-04. Pair with [RELEASE_READINESS](RELEASE_READINESS.md),
[SECURITY](SECURITY.md), [SAFETY](SAFETY.md) and [COMPLIANCE](COMPLIANCE.md).
[ADR-0032](adr/0032-complete-native-go-backend.md) records approved implementation landing;
[STATUS](../STATUS.md) remains the authority for deployment and product launch gates.

## Release and configuration

Use the reviewed native release artifact: executable, precompiled static assets,
templates, locale and required reference data. Verify its exact digest and native CI
qualification before installation. The [deployment templates](../deploy/README.md) are
unapplied examples, not evidence of a live deployment or a purchased provider.

The serving executable reads explicit environment settings, not `.env` automatically.
Systemd may supply its protected `EnvironmentFile`; values come only through the approved
secrets workflow. Required names and supported policies are documented in the
[CLI reference](../services/server/cmd/social-server/README.md). Configuration errors report
names, never values. Do not inspect real env/auth stores or paste credentials into logs.

Before an authorized rollout, retain the old artifact and verified database recovery path,
then run `social-server --migrate-only` against the explicitly selected database. Native
bootstrap/adoption retains existing domain rows and preinstalled extension schemas;
legacy sessions are retired and users sign in again. Test rollback on a recovered fixture
before a real migration. Do not reset/drop a database to make bootstrap succeed.

The executable serves HTTP/WebSockets behind the reviewed TLS proxy. Set the exact
canonical origin, allowed hosts and trusted immediate-proxy CIDRs. Forwarded identity and
HTTPS headers are accepted only across that trust boundary. PostgreSQL live notifications
carry IDs and trigger fresh permission checks. Shared API/domain admission uses PostgreSQL;
run the additive native migration before serving the completion release. Missing rate schema
stops startup, and admission errors refuse work. Required-shared mode verifies that contract;
Redis remains unused/refused. Optional Sentry emits only fixed error classes/coarse routes
with bounded queues and shutdown. Configure its approved destination and alerts separately.
See [SCALING](SCALING.md) and the CLI guide for policy/retired-setting validation.

Production private media needs approved EU/private-bucket verification, effective scanner
configuration and native codecs. Local storage is loopback development only. Initial
administrator creation is the explicit private-stdin `createsuperuser` command; it creates
a fresh unverified/unassigned administrator and grants no age or parental assurance.
The guarded account permission form requires current eligible manager authority; effective
capability changes revoke sessions/API tokens and commit with audit. Use its complete preset
workflow, never raw identity/cohort/consent edits.
Provider activation, ingestion, paid infrastructure and minor onboarding retain separate
owner/product gates.

## Health and monitoring

- `GET /healthz` is process liveness; it does not check the database. Only direct loopback
  liveness has the narrow HTTP-redirect exception, still subject to allowed-host checks.
- `GET /readyz` checks PostgreSQL, configured dependency checks and draining state. It can
  return 503 while liveness remains 200. Readiness does not prove every provider is usable.
- `GET /api/ops/stats/` is staff-only, aggregate-only operational state.
- `GET /metrics` requires the configured metrics bearer credential and exposes aggregate
  request/error/duration counters. Do not publish its credential or add per-user analytics.
- Native request logs contain method, static route pattern, status and duration with safe
  correlation IDs; private paths, query values, IP/user identities, headers and bodies stay
  out. Queue failure diagnostics omit payload/error content.

On shutdown, mark draining, allow the bounded HTTP drain, then cancel/wait for background
media and release live/storage/database resources. Investigate unexpected readiness loss,
failed task batches and video processing age with aggregate diagnostics and audited staff
views, without retrieving private message/media content.

## Backups and restore

Preserve the established nightly PostgreSQL backup, at-least-30-day retention and quarterly
restore-test targets. Store backups in approved EU infrastructure with restricted access.
Keep encryption/key recovery and media deletion policy aligned with the privacy procedure;
bucket versioning/lifecycle must not defeat lawful erasure or evidence holds.

Restore into an explicitly isolated recovery database, verify the backup and extensions,
run native migration/adoption, then exercise readiness and synthetic sign-in/CSRF/domain
checks. Reconcile private media/object references and cleanup continuation. A successful
SQL restore alone is not a recovery drill; never smoke-test with real users' credentials.

## Safety and incident response

1. Detect through the native staff moderation/report queue, security alerts and aggregate
   abuse signals. Preserve the audited record of who accessed or acted on a case.
2. Triage child-safety/CSAM at highest priority. Preserve evidence through governed holds;
   do not download or redistribute suspect media. A blocked quarantined source has no
   in-app byte URL, including for staff.
3. Contain through native governed moderation: deactivate/sanction accounts, remove
   content and revoke access. Actions commit with the hash-chained audit; the native
   `safety.Service.VerifyAuditChain` verifies integrity. Do not raw-edit safety/identity rows.
4. Follow the existing compliance process for required legal/LEA reporting, credible threats
   and DPO escalation, including its personal-data breach clock. Recording a referral
   inside the application does not send a report to any authority.
5. Recover access only through reviewed transitions, preserve lawful holds, and document
   the incident and corrective verification without raw personal content or credentials.

### Sanction durations

| Sanction | Duration | Lift behavior | Identity ban ledger |
|---|---|---|---|
| `SUSPEND` with days | Until expiry | Eligible for `lift_suspensions` | No |
| `SUSPEND` without days | Indefinite | Never auto-lifts | No |
| `TIMED_BAN` | Days required | Eligible for `lift_suspensions` | No |
| `BAN` | Lifetime | No automatic lift | Yes; survives account erasure |

`TIMED_BAN` without `suspend_days` is rejected. An indefinite suspension is reversible by
an authorized manual lift or successful appeal, but does not create the lifetime identity
ban that prevents wallet re-registration. Scheduled lifting occurs only when the explicit
`lift_suspensions` job is actually run; no startup scheduler is implied.

### Authority referrals

The native referral and audited proof endpoint maintain an internal tamper-evident ledger
and lawful-request proof bundle. They do not transmit to a hotline, law enforcement or
another authority. The on-call moderator owns the existing out-of-band reporting duty,
including the applicable national hotline/INHOPE or law-enforcement route, and recording
the external case/reference alongside the referral through supported audited operations.
A referral with no external report completed remains an open compliance task.

The subject is not notified of the referral, to preserve investigations; an associated
account sanction still has its own safety/DSA notice. Do not treat the absence of a
transmission integration as completion of the external duty.

## Explicit maintenance

`social-server --due` runs the 27 registered due jobs once, including retention, expiry,
suspension lifting, reminders and deferred cleanup, then exits. Authorized systemd timers
may schedule that command; templates do not activate themselves. Use
`social-server --job transcode_videos` for a bounded video pass, or
`social-server --job process_deferred_tasks` for a queue pass. Options and limits are in
[ASYNC_TASKS](ASYNC_TASKS.md) and the CLI reference.

E2EE messaging retention uses `purge_messaging` when configured. Activity/group thread
posts are governed domain records; the retired `purge_chat` command is not a native
maintenance path. Erasure removes relational/key/ciphertext access synchronously and
retains durable retries for physical object deletion. Inspect failed deletion tasks and
outbox backlog rather than deleting their evidence of pending work.

Audit native source/package/binary dependencies and the release image with the checked-in
CI gates; review advisory reachability and resolved versions. Python audits qualify the
offline oracle only. Donation reconciliation stays inside the configured native provider
and audited ledger transitions; a job cannot authorize a real payment provider.

The complete earlier runbook is retained as [historical reference](archive/runbook-native-go-reference.md).
