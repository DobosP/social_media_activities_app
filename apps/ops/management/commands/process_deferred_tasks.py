"""Drain the durable deferred-task queue — the worker side of the off-request foundation.

Claims due ``DeferredTask`` rows and runs their handlers (with bounded backoff retries). Invoked
each tick by ``run_due_jobs`` so no separate process is required; also runnable on its own
(``python manage.py process_deferred_tasks``) for local dev or a dedicated worker later. Idempotent
and safe to run concurrently (claims use ``SELECT ... FOR UPDATE SKIP LOCKED``).
"""

from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "Claim and run due deferred tasks (the off-request work foundation)."

    def add_arguments(self, parser):
        parser.add_argument(
            "--limit",
            type=int,
            default=None,
            help="Max tasks to process this run (default: settings.DEFERRED_TASKS_BATCH).",
        )

    def handle(self, *args, **options):
        from apps.ops.tasks import run_pending_tasks

        s = run_pending_tasks(limit=options.get("limit"))
        msg = (
            f"deferred tasks: claimed={s['claimed']} done={s['done']} "
            f"retried={s['retried']} failed={s['failed']}"
        )
        # A failed task already logged + reported to Sentry inside run_pending_tasks; surface the
        # count on stderr so a cron tail notices, but don't raise (a poison task must not wedge the
        # whole drain — the rest of the batch still ran).
        if s["failed"]:
            self.stderr.write(self.style.WARNING(msg))
        else:
            self.stdout.write(self.style.SUCCESS(msg))
