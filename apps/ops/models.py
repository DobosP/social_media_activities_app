"""Durable off-request task foundation — the Postgres-backed seam for moving heavy or
latency-sensitive work off the request path.

`DeferredTask` is the ONLY model here. It owns no business logic: it is a tiny, durable work
queue so that GDPR-erasure blob cleanup, media scanning, or large notification fan-outs can run
*after* the request commits, on the existing cron worker, WITHOUT adding a broker (Celery/RQ) —
Postgres stays the single datastore (inv.6). See ``apps.ops.tasks`` for the enqueue/run API and
``docs/ASYNC_TASKS.md`` for the contract + the concrete plan for the first real callers.
"""

from __future__ import annotations

from django.db import models
from django.db.models import Q
from django.utils import timezone


class DeferredTask(models.Model):
    """One unit of durable, off-request work.

    Lifecycle: a row is created PENDING by ``apps.ops.tasks.enqueue`` inside the caller's
    transaction (transactional enqueue — a rolled-back request enqueues nothing), then claimed and
    run later by the ``process_deferred_tasks`` worker (fanned out by the ``run_due_jobs`` cron).
    On success it becomes DONE; on a handler error it is retried with backoff up to ``max_attempts``
    and then marked FAILED. ``kind`` maps to a handler registered via ``apps.ops.tasks.register``.

    Handlers MUST be idempotent: the worker guarantees *at-least-once* execution (a process killed
    mid-run leaves the row PENDING to be retried), never exactly-once.
    """

    class Status(models.TextChoices):
        PENDING = "PENDING", "Pending"
        DONE = "DONE", "Done"
        FAILED = "FAILED", "Failed"

    kind = models.CharField(max_length=64, db_index=True)
    # JSON-serialisable handler input. Carry IDs / minimal data only — NEVER bulk PII or secrets:
    # rows are stored in the clear in Postgres (see docs/ASYNC_TASKS.md, "What goes in a payload").
    payload = models.JSONField(default=dict, blank=True)
    status = models.CharField(
        max_length=16, choices=Status.choices, default=Status.PENDING, db_index=True
    )
    attempts = models.PositiveIntegerField(default=0)
    max_attempts = models.PositiveSmallIntegerField(default=5)
    # Earliest time the task may run — drives both an initial delay and exponential retry backoff.
    available_at = models.DateTimeField(default=timezone.now)
    # Optional idempotency key: at most one PENDING row per (kind, dedup_key) (DB-enforced below),
    # so "schedule cleanup for user X" can be enqueued repeatedly without piling up duplicates.
    dedup_key = models.CharField(max_length=200, blank=True, default="")
    # Bounded summary of the last failure (type + message, truncated). Diagnostics only.
    last_error = models.TextField(blank=True, default="")
    created_at = models.DateTimeField(auto_now_add=True)
    started_at = models.DateTimeField(null=True, blank=True)
    finished_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        ordering = ("available_at", "id")
        indexes = [
            # The claim query: PENDING rows whose time has come, oldest first.
            models.Index(fields=["status", "available_at"], name="ops_task_claim_idx"),
        ]
        constraints = [
            # At-most-one live (PENDING) task per (kind, dedup_key). Excludes the empty default so
            # un-keyed tasks never collide. Backstops the check-then-create race in enqueue().
            models.UniqueConstraint(
                fields=["kind", "dedup_key"],
                condition=Q(status="PENDING") & ~Q(dedup_key=""),
                name="ops_task_unique_pending_dedup",
            ),
        ]

    def __str__(self) -> str:
        return f"DeferredTask(id={self.pk}, kind={self.kind!r}, status={self.status})"
