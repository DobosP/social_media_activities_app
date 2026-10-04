# Native deferred work and explicit jobs

Verified against native Go on 2026-10-04. The queue implementation is
[internal/ops/tasks.go](../services/server/internal/ops/tasks.go); registered handlers and
periodic work are in [internal/jobs](../services/server/internal/jobs/).
[ADR-0032](adr/0032-complete-native-go-backend.md) records the runtime boundary and
[STATUS](../STATUS.md) records activation gates.

## Safety contract

Deferral moves work whose outcome is already authorized. Cohort, consent, membership,
blocking, privacy and moderation checks remain on the operation's domain path. Media
must remain unviewable until every required effective scan and processing gate succeeds.
Moving scan execution to a worker never permits admit-first/scan-later behavior.

Suitable deferred work includes physical byte deletion after authorized row erasure,
bounded notification retention and fan-out that rechecks current recipients. A task's
existence is not proof of permission, successful external reporting, or media cleanliness.
Safety/DSA notices that must be immediate remain synchronous.

## Queue mechanics

`ops.NewQueue` binds a PostgreSQL pool. `Queue.Register` installs a concrete
`TaskHandler(context.Context, pgx.Tx, map[string]json.RawMessage)`; unknown kinds and nil
handlers are refused. `Queue.Enqueue` inserts in the caller's transaction, so rollback
leaves no task. Payloads are JSON objects capped at 64 KiB; dedup keys are bounded.
An advisory transaction lock and pending-row uniqueness protect `(kind, dedup_key)`.

`Queue.RunPending(ctx, limit)` claims due rows with `FOR UPDATE SKIP LOCKED`. There is
one drainer per queue instance to protect the small connection pool; independent
processes can claim different rows. Handler writes use a savepoint. On failure they
roll back, while the task attempt/retry or terminal failure record commits. Failure
summaries retain the error type and a generic diagnostic, not raw payload/error content.

Delivery is at least once. A process interruption or an external side effect before a
failed commit can cause a repeat; every handler must be idempotent. Attempts default to
five, with exponential backoff starting at 30 seconds and capped at one hour. Runtime
configuration can adjust the supported backoff settings; unsupported policy overrides
fail startup by name. Exhausted tasks stay `FAILED` for operator review. Sentry is not a
native reporting path; `SENTRY_DSN` refuses unsupported configuration.

## Payloads and current handlers

Use IDs and minimal bounded scalars. No credentials, tokens, uploaded bytes, ciphertext
or private conversation bodies belong in a queue payload. A handler reloads referenced
state and tolerates changed/deleted rows. Opaque storage keys are confined to private
cleanup tasks; bounded notification presentation fields are not a private-chat channel.

| Registered kind | Current behavior |
|---|---|
| `erasure.blob_cleanup` | Idempotent deletion of private object keys; bounded chunks enqueue their complete remainder and audit completion |
| `notify.activity_fanout` | Re-derives current visible activity membership, excludes the actor/blocked pairs, dedups and calls `platform.Notify`; current pass caps recipients at 500 |
| `notifications.retention_purge` | Deletes a bounded batch of old read mutable notices; unread and safety/DSA kinds remain protected |
| `cron.run_command` | Runs only allowlisted due jobs, excluding recursive `process_deferred_tasks`; never arbitrary shell/Python commands |
| `media.scan.dispatch` | Audits a blocked dispatch and changes no cleanliness/visibility; this is not an asynchronous image/PDF scanner |

The last kind deliberately grants nothing. Images/PDFs still require effective clean
admission synchronously. A future asynchronous image/PDF implementation needs a reviewed
withheld-state lifecycle, current authorization and clean-only finalization before it can
replace that gate. Pending video processing is a separate implemented lifecycle.

## Erasure and media continuation

Account erasure removes the authorized relational graph, keys/ciphertext and access
synchronously. Media delete triggers capture main, thumbnail, poster and quarantined
source references in a durable deletion outbox. The outbox and `erasure.blob_cleanup`
retain physical cleanup through a storage outage; a failed object deletion does not
restore erased rows or discard the obligation. Partial/ambiguous remote stores and an
abandoned prepared attachment also retain cleanup rather than silently orphaning bytes.

Video admission retains a private pending source. After a successful upload/post commit,
`MEDIA_VIDEO_INLINE_PROCESSING=true` can trigger one application-lifetime, single-flight
pass of at most two queued videos. Startup and migration trigger no such pass; false
disables the upload kick. The bounded `transcode_videos` job/timer retries interrupted or
stale work. Required frame scans and current domain checks precede a ready attachment.
See [FILE_STORAGE](FILE_STORAGE.md) for failure/evidence and serving behavior.

## Operator commands

```sh
social-server --due
social-server --job process_deferred_tasks
social-server --job process_deferred_tasks --job-options -
social-server --job transcode_videos --job-options -
```

A reviewed options object can set `limit` for those individual jobs; CLI input limits,
unknown-key rejection and exact command options are in the
[CLI reference](../services/server/cmd/social-server/README.md). `--due` runs the 27 concrete
`jobs.DueNames` entries once, with `process_deferred_tasks` last, then exits. No command
installs or activates a recurring scheduler. The checked-in systemd job/video timers
remain unapplied until authorized; one-shot jobs do not reserve the HTTP live listener.

Monitor aggregate queue age, claimed/done/retried/failed counts, outbox backlog and video
processing age. Reconcile terminal failures through audited operator procedures; do not
clear the queue/outbox to hide a backlog. Scheduling real source-sync/provider work,
paid infrastructure, minors and deployment retain their separate gates.

See [RUNBOOK](RUNBOOK.md) and [SCALING](SCALING.md). The complete earlier queue guide is
retained as [historical reference](archive/async-tasks-native-go-reference.md).
