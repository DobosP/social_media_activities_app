from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.social.models import ActivityInterest


class Command(BaseCommand):
    help = "Delete expired gauge-interest rows (F27) so a gauge stays ephemeral and self-heals."

    def handle(self, *args, **opts):
        # Deletes both lapsed-unconverted gauges AND converted ones past their window (the
        # spawned Activity stands on its own; the gauge row is no longer needed). Cascades the
        # interested_users M2M rows. `delete()` returns the total incl. through rows.
        total, _ = ActivityInterest.objects.filter(expires_at__lt=timezone.now()).delete()
        self.stdout.write(self.style.SUCCESS(f"expired gauges deleted: {total}"))
