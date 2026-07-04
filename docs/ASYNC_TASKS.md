# Off-request work — the durable deferred-task foundation

**Code-grounded as of 2026-07-04.** Ships the Postgres-backed queue
(`apps/ops/tasks.py`, `apps/ops/models.py:DeferredTask`, the `process_deferred_tasks` command) plus
the first production task kinds in `apps/ops/handlers.py`: `erasure.blob_cleanup`,
`notify.activity_fanout`, `cron.run_command`, and the fail-closed `media.scan.dispatch` placeholder.

## Why this exists

Some work is too heavy or too latency-sensitive to do inside the web request:

- **GDPR erasure** deletes a whole account graph and signals object-store blob cleanup — slow, and
  an S3 hiccup should never roll back a lawful erasure.
- **Media scanning** of a freshly-uploaded image/PDF can be slow (an external scanner round-trip).
- **Notification fan-out** to every member of a large activity/group is O(members) inserts on the
  request thread.

The product invariants constrain the solution: **Postgres is the single datastore** (inv.6) and we
**avoid heavy deps and per-user cloud spend**. So this is a *Postgres-backed* work queue drained by
the existing `run_due_jobs` cron — **no Celery/RQ, no Redis-as-broker, no new process required.**
When volume genuinely outgrows a cron tick, the *same* `enqueue`/handler API can be pointed at a
dedicated worker loop or a real broker without touching call sites — that is the point of the seam.

## The contract

```python
from apps.ops.tasks import register, enqueue

@register("erasure.blob_cleanup")          # bind a handler to a string kind (in apps/ops/handlers.py)
def _cleanup(payload: dict) -> None:
    ...                                     # IDEMPOTENT unit of work; takes IDs, not objects

enqueue("erasure.blob_cleanup", {"user_id": 42}, dedup_key="user:42")
```

- **Transactional enqueue.** `enqueue()` writes one `PENDING` row **in the caller's transaction**.
  A rolled-back request enqueues nothing; a committed one is guaranteed to have its task durably
  recorded. Call it *inside* the `@transaction.atomic` service that triggers the work.
- **At-least-once, never exactly-once.** A handler may run more than once (a retry after a transient
  error, or a worker killed mid-run leaving the row `PENDING`). **Every handler MUST be idempotent.**
- **Bounded retries.** A handler that raises is retried with exponential backoff
  (`DEFERRED_TASKS_BACKOFF_BASE`, capped at `DEFERRED_TASKS_MAX_BACKOFF`) up to `max_attempts`
  (default `DEFERRED_TASKS_MAX_ATTEMPTS`), then marked `FAILED` and reported to Sentry. A handler's
  own DB writes roll back on failure (savepoint); the task's failure record still commits.
- **Idempotent enqueue.** `dedup_key` enforces *at most one PENDING task* per `(kind, dedup_key)` —
  both in `enqueue()` and as a partial unique constraint (the race backstop). "Schedule cleanup for
  user X" can be fired repeatedly without piling up duplicates.
- **Concurrency-safe.** Claims use `SELECT ... FOR UPDATE SKIP LOCKED`, so two drainers never run
  the same task.
- **`kind` must be registered** before `enqueue` (fail-fast on typos). Handlers live in
  `apps/ops/handlers.py`, imported at startup by `OpsConfig.ready`.

## ⚠️ Safety rule — what you may and may NOT defer

> **Deferral only moves work that is *already authorised*. A gate that must hold BEFORE an action
> takes effect can never be moved behind a deferred task.**

This is the load-bearing rule for child-safety/privacy (inv.3, inv.4). Concretely:

- ✅ Safe to defer: work that happens **after** the user-visible outcome is already correct and
  gated — orphan-blob cleanup after the rows are deleted, fan-out of a notification whose per-
  recipient mute/block gate is re-checked at send time, scanning of media that stays **unviewable
  until the scan passes**.
- ❌ Never defer: the **fail-closed scan gate itself**. `apps/media/services.py` rejects media
  unless the scan is *effective and clean* (`MEDIA_REQUIRE_SCANNER`) before the `Attachment` is
  readable. You may move the *scan execution* off-request only if the attachment is **withheld**
  (not `can_view_attachment`-visible) until the deferred scan completes and flips it. Admitting
  media first and scanning later is a child-safety regression — do not do it.
- ❌ Never defer a **cohort/consent/block check** to "later". Those decide whether an action is
  allowed at all; they belong on the request path.

## What goes in a payload

JSON-serialisable **IDs and minimal scalars only.** Rows are stored in the clear in Postgres, so:

- No bulk PII, message bodies, photos, tokens, or secrets in a payload — pass a `user_id` /
  `post_id`, and have the handler re-load (and re-authorise) from the DB.
- A handler must tolerate the referenced row having changed or vanished between enqueue and run
  (re-check, no-op gracefully) — that falls out of the idempotency requirement.

## Operating it

- **Drained by cron.** `process_deferred_tasks` is the last job in `run_due_jobs` `DUE_JOBS`, so the
  existing scheduler drains the queue every tick. Run it standalone for dev:
  `python manage.py process_deferred_tasks [--limit N]`.
- **Settings** (`config/settings/base.py`): `DEFERRED_TASKS_BATCH` (per-pass cap),
  `DEFERRED_TASKS_MAX_ATTEMPTS`, `DEFERRED_TASKS_BACKOFF_BASE`, `DEFERRED_TASKS_MAX_BACKOFF`.
- **Observability.** Each failed attempt logs a warning with the run-correlation `request_id`
  (the cron stamps one); an exhausted task is captured to Sentry tagged `deferred_task_kind`.
  `DeferredTask.last_error` keeps a bounded last-failure summary for triage.
- **Latency.** Cron-tick latency is fine for cleanup/fan-out. If a future kind needs near-real-time
  draining, add a dedicated `process_deferred_tasks` worker loop (the API is unchanged).

## Concrete migration plan for the first callers

Pick **one** as the first real handler; each is a small, reviewable change.

### 1. GDPR-erasure blob cleanup *(implemented)*
- **Today:** `apps.accounts.services.erase_user` deletes the account graph synchronously; photo,
  attachment, and activity-cover row deletes enqueue `erasure.blob_cleanup` inside the delete
  transaction. The privacy guarantee (rows gone) is synchronous and stays synchronous.
- **Handler:** `erasure.blob_cleanup` accepts a bounded `blob_keys` list, deletes each object-store
  key idempotently, and writes an audit row with the blob count. A storage outage now retries
  instead of failing the user's erasure.
- **Safety:** no gate moves — the user's data is already unreachable the instant the rows commit.

### 2. Media scanning *(fail-closed placeholder only)*
- **Today:** `apps.media.services.attach_to_post` scans **fail-closed before** the attachment is
  viewable. Keep that gate.
- **Current deferred kind:** `media.scan.dispatch` records an audited no-op and does not mark
  anything clean or visible, because there is no withheld media state yet. Synchronous scan remains
  the admission gate.
- **Future change (only if needed for latency):** persist the attachment in a **withheld** state,
  then enqueue a real scanner task. The handler may flip the attachment to viewable **only on a
  clean result**; a non-clean/ineffective result keeps it withheld. **`can_view_attachment` must
  treat un-scanned as not-viewable.** Never admit-then-scan.

### 3. Notification fan-out *(first kind implemented)*
- **Today:** `apps.social.services.post_announcement` and member notifications loop
  `notifications.notify` per recipient on the request path; `notify` applies the per-recipient
  mute/DSA gate and blocked-pair exclusion at send time.
- **Current kind:** `notify.activity_fanout` re-derives current activity members, excludes the
  actor and blocked pairs, dedups by `(recipient, kind, url, title)`, and calls the
  `notifications.notify()` chokepoint so mutable preferences and DSA non-mutable carve-outs stay
  live. DSA-mandated MODERATION/SYSTEM notices that must be immediate should stay synchronous.
- **Safety:** the mute/block/DSA gate stays inside `notify` and is re-evaluated at send time, so
  deferral changes only *when*, never *who*.
