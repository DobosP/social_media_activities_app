"""Delete old, READ, non-safety in-app notifications (storage hygiene as fan-out grows).

A notification is a convenience pointer, not a record — EXCEPT the DSA-mandated MODERATION (Art.17)
and SYSTEM (Art.16) notices, which are NEVER purged. So this deletes only notifications the user has
already READ (``read_at`` set) that are older than ``NOTIFICATION_RETENTION_DAYS`` and are NOT a
non-mutable DSA kind. Idempotent + safe to re-run; ``NOTIFICATION_RETENTION_DAYS=0`` disables it.
"""

from datetime import timedelta

from django.conf import settings
from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.notifications.models import NON_MUTABLE_KINDS, Notification


class Command(BaseCommand):
    help = "Delete read, non-safety notifications older than NOTIFICATION_RETENTION_DAYS."

    def handle(self, *args, **options):
        days = getattr(settings, "NOTIFICATION_RETENTION_DAYS", 180)
        if not days or days <= 0:
            self.stdout.write("Notification retention disabled (NOTIFICATION_RETENTION_DAYS=0).")
            return
        cutoff = timezone.now() - timedelta(days=days)
        deleted, _ = (
            Notification.objects.filter(read_at__isnull=False, created_at__lt=cutoff)
            .exclude(kind__in=[k.value for k in NON_MUTABLE_KINDS])  # keep DSA notices forever
            .delete()
        )
        self.stdout.write(
            self.style.SUCCESS(f"Purged {deleted} read notification(s) older than {days}d.")
        )
