"""Schedule deletion of old, READ, non-safety in-app notifications.

A notification is a convenience pointer, not a record — EXCEPT the DSA-mandated MODERATION
(Art.17) and SYSTEM (Art.16) notices, which are NEVER purged. So this schedules a bounded deferred
task that deletes only notifications the user has already READ (``read_at`` set), older than
``NOTIFICATION_RETENTION_DAYS``, and NOT a non-mutable DSA kind. Idempotent + safe to re-run;
``NOTIFICATION_RETENTION_DAYS=0`` disables it.
"""

from django.conf import settings
from django.core.management.base import BaseCommand

from apps.ops import handlers as _handlers  # noqa: F401  ensure production kinds are registered
from apps.ops.tasks import enqueue


class Command(BaseCommand):
    help = "Schedule deletion of read, non-safety notifications past retention."

    def handle(self, *args, **options):
        days = getattr(settings, "NOTIFICATION_RETENTION_DAYS", 180)
        if not days or days <= 0:
            self.stdout.write("Notification retention disabled (NOTIFICATION_RETENTION_DAYS=0).")
            return
        batch_size = getattr(settings, "NOTIFICATION_RETENTION_BATCH", 1000)
        enqueue(
            "notifications.retention_purge",
            {"days": days, "batch_size": batch_size},
            dedup_key="notifications:retention_purge",
        )
        self.stdout.write(
            self.style.SUCCESS(
                f"Scheduled notification retention purge for read notices older than {days}d "
                f"(batch={batch_size})."
            )
        )
