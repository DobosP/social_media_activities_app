"""Durable, off-request task foundation — Postgres-backed, no broker, no new dependency.

This is the smallest safe seam for moving heavy or latency-sensitive work (GDPR-erasure blob
cleanup, media scanning, large notification fan-outs) OFF the request path. It deliberately does
NOT introduce Celery/RQ/Redis-as-broker — Postgres stays the single datastore (inv.6) and the
existing ``run_due_jobs`` cron is the worker. See ``docs/ASYNC_TASKS.md`` for the full contract and
the concrete migration plan for the first real callers.

Three pieces:

* ``register(kind)`` / ``@register`` — bind a handler to a string ``kind``.
* ``enqueue(kind, payload, ...)`` — durably record a unit of work in the CURRENT transaction
  (transactional enqueue: it commits atomically with the business write that triggered it).
* ``run_pending_tasks()`` — claim due tasks (``SELECT ... FOR UPDATE SKIP LOCKED``) and run them,
  with bounded exponential-backoff retries. Driven by the ``process_deferred_tasks`` command.

GUARANTEE: at-least-once. A handler may run more than once (retry after a transient failure, or a
worker killed mid-run leaving the row PENDING). **Every handler MUST be idempotent.** Nothing here
weakens a safety gate: deferral only moves *already-authorised* work; a gate that must hold before
an action (e.g. fail-closed media scanning) must NOT be moved behind a deferred task — see the doc.
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from datetime import timedelta

from django.conf import settings
from django.db import IntegrityError, transaction
from django.utils import timezone

logger = logging.getLogger("apps.ops.tasks")

# kind (str) -> handler. A handler takes the decoded JSON payload (a dict) and performs ONE
# idempotent unit of work. Populated at import time by register()/@register.
_REGISTRY: dict[str, Callable[[dict], None]] = {}


# --- registry ---------------------------------------------------------------------------------


def register(kind: str) -> Callable[[Callable[[dict], None]], Callable[[dict], None]]:
    """Decorator binding ``fn`` as the handler for ``kind``. Raises on a duplicate kind so two
    handlers can never silently shadow each other (re-registering the SAME callable — e.g. a module
    re-imported under autoreload — is a no-op)."""

    def _decorator(fn: Callable[[dict], None]) -> Callable[[dict], None]:
        existing = _REGISTRY.get(kind)
        if existing is not None and existing is not fn:
            raise ValueError(f"deferred-task kind {kind!r} is already registered")
        _REGISTRY[kind] = fn
        return fn

    return _decorator


def registered_kinds() -> frozenset[str]:
    """The currently-registered task kinds (handy for tests and ops introspection)."""
    return frozenset(_REGISTRY)


# --- enqueue ----------------------------------------------------------------------------------


def enqueue(
    kind: str,
    payload: dict | None = None,
    *,
    delay: timedelta | None = None,
    available_at=None,
    max_attempts: int | None = None,
    dedup_key: str = "",
):
    """Durably enqueue a unit of off-request work and return the ``DeferredTask`` row.

    The row is written in the CURRENT database transaction — so the task is committed atomically
    with whatever business write triggered it (a rolled-back request enqueues nothing; a committed
    one is guaranteed to have its task durably recorded). A separate worker runs it later.

    ``kind`` MUST already be registered (fail fast on a typo — never silently swallow). ``payload``
    must be JSON-serialisable and should carry IDs / minimal data, never bulk PII or secrets (it is
    stored in the clear). ``delay``/``available_at`` schedule it for the future. ``dedup_key`` makes
    enqueue idempotent: if a PENDING row with the same ``(kind, dedup_key)`` already exists, that
    row is returned instead of creating a duplicate.
    """
    from .models import DeferredTask

    if kind not in _REGISTRY:
        raise ValueError(f"no handler registered for deferred-task kind {kind!r}")
    payload = payload or {}
    if max_attempts is None:
        max_attempts = getattr(settings, "DEFERRED_TASKS_MAX_ATTEMPTS", 5)

    if available_at is not None:
        when = available_at
    elif delay is not None:
        when = timezone.now() + delay
    else:
        when = timezone.now()

    # Fast-path dedup: skip a duplicate before touching the DB write. The partial unique constraint
    # (kind, dedup_key WHERE status=PENDING) is the race backstop below.
    if dedup_key:
        existing = DeferredTask.objects.filter(
            kind=kind, dedup_key=dedup_key, status=DeferredTask.Status.PENDING
        ).first()
        if existing is not None:
            return existing

    try:
        with transaction.atomic():  # savepoint so a constraint race doesn't poison the caller txn
            return DeferredTask.objects.create(
                kind=kind,
                payload=payload,
                available_at=when,
                max_attempts=max_attempts,
                dedup_key=dedup_key,
            )
    except IntegrityError:
        # Lost a race on the partial unique constraint — return the row the winner created.
        return DeferredTask.objects.filter(
            kind=kind, dedup_key=dedup_key, status=DeferredTask.Status.PENDING
        ).first()


# --- run --------------------------------------------------------------------------------------


def _backoff_seconds(attempts: int) -> int:
    """Exponential backoff (deterministic — a single drainer + SKIP LOCKED needs no jitter),
    capped. attempts is 1-based (the attempt that just failed)."""
    base = getattr(settings, "DEFERRED_TASKS_BACKOFF_BASE", 30)
    cap = getattr(settings, "DEFERRED_TASKS_MAX_BACKOFF", 3600)
    return min(base * (2 ** max(0, attempts - 1)), cap)


def _format_error(exc: Exception) -> str:
    return f"{type(exc).__name__}: {exc}"[:2000]


def run_pending_tasks(*, limit: int | None = None) -> dict:
    """Claim and run up to ``limit`` due PENDING tasks (oldest-available first), each in its own
    transaction so one task's outcome never rolls back another's. Returns a summary dict
    ``{claimed, done, retried, failed}``. Safe to call repeatedly and concurrently — every claim
    uses ``SELECT ... FOR UPDATE SKIP LOCKED`` so two workers never run the same task."""
    if limit is None:
        limit = getattr(settings, "DEFERRED_TASKS_BATCH", 100)
    summary = {"claimed": 0, "done": 0, "retried": 0, "failed": 0}
    for _ in range(max(0, limit)):
        outcome = _run_one()
        if outcome is None:
            break  # nothing left that is due and unlocked
        summary["claimed"] += 1
        summary[outcome] += 1
    return summary


def _run_one() -> str | None:
    """Claim and run a single due task. Returns 'done'/'retried'/'failed', or None if none due."""
    from .models import DeferredTask

    now = timezone.now()
    with transaction.atomic():
        task = (
            DeferredTask.objects.select_for_update(skip_locked=True)
            .filter(status=DeferredTask.Status.PENDING, available_at__lte=now)
            .order_by("available_at", "id")
            .first()
        )
        if task is None:
            return None

        task.attempts += 1
        if task.started_at is None:
            task.started_at = now
        handler = _REGISTRY.get(task.kind)
        try:
            if handler is None:
                # Missing handler (typo, de-registered kind, or a deploy mid-rollout). Treat as a
                # normal failure so a transient import gap self-heals, then exhausts to FAILED.
                raise LookupError(f"no handler registered for deferred-task kind {task.kind!r}")
            # Savepoint: a handler DB error rolls back the handler's OWN writes while leaving the
            # outer transaction healthy enough to record the failure below.
            with transaction.atomic():
                handler(dict(task.payload or {}))
        except Exception as exc:  # noqa: BLE001 — bounded retry is the whole point
            return _record_failure(task, exc, now)

        task.status = DeferredTask.Status.DONE
        task.finished_at = now
        task.last_error = ""
        task.save(update_fields=["status", "attempts", "started_at", "finished_at", "last_error"])
        return "done"


def _record_failure(task, exc: Exception, now) -> str:
    from .models import DeferredTask

    task.last_error = _format_error(exc)
    logger.warning(
        "deferred task %s (kind=%s) attempt %d/%d failed: %s",
        task.pk,
        task.kind,
        task.attempts,
        task.max_attempts,
        exc,
    )
    if task.attempts >= task.max_attempts:
        task.status = DeferredTask.Status.FAILED
        task.finished_at = now
        task.save(update_fields=["status", "attempts", "started_at", "finished_at", "last_error"])
        # A dead erasure/scan/notify task is a compliance/safety miss — surface it, don't bury it.
        _capture(exc, task)
        return "failed"

    # Stay PENDING; just back off and let the next drain pick it up.
    task.available_at = now + timedelta(seconds=_backoff_seconds(task.attempts))
    task.save(update_fields=["attempts", "started_at", "available_at", "last_error"])
    return "retried"


def _capture(exc: Exception, task) -> None:
    """Report a permanently-failed task to Sentry, tagged by kind. No-op without Sentry."""
    try:
        import sentry_sdk

        with sentry_sdk.new_scope() as scope:
            scope.set_tag("deferred_task_kind", task.kind)
            scope.set_tag("deferred_task_id", task.pk)
            sentry_sdk.capture_exception(exc)
    except Exception:  # noqa: BLE001 — reporting must never break the worker
        pass
