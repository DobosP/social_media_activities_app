"""Production deferred-task handlers — the seam where the first real callers will live.

This file is imported at app startup (see ``OpsConfig.ready``) so every ``@register`` runs and the
registry is populated before any ``enqueue`` call. It is intentionally EMPTY of production handlers
today: this PR ships only the foundation (model + enqueue/run API + worker command). The next PR
adds the first handler here, following the concrete plan in ``docs/ASYNC_TASKS.md``.

To add one::

    from apps.ops.tasks import register

    @register("erasure.blob_cleanup")
    def _erasure_blob_cleanup(payload: dict) -> None:
        # IDEMPOTENT: safe to run more than once. Take IDs from `payload`, not objects.
        ...

Keep handlers thin: validate the payload, call an existing service, return. No business logic here.
"""

from __future__ import annotations

# (no production handlers registered yet — see docs/ASYNC_TASKS.md for the migration plan)
