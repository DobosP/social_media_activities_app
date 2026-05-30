"""Clear stale arrival pings (F3) so they never become a standing presence record.

An arrived_at timestamp is wiped once the activity's start is more than --retention-hours in
the past — long enough that the ping is useful at meetup time, short enough that it is not a
durable "who was where" log. Idempotent: only rows with a set arrived_at are touched, and the
bulk update never re-pings anyone. Intended for the shared run_due_jobs cron tick.
"""

from datetime import timedelta

from django.conf import settings
from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.social.models import Membership

# Must exceed ARRIVAL_WINDOW_AFTER_HOURS so a ping survives its window but is cleared a few
# hours after the meetup starts.
DEFAULT_ARRIVAL_RETENTION_HOURS = 6


class Command(BaseCommand):
    help = "Clear arrival pings older than the retention window (keeps the ping ephemeral)."

    def add_arguments(self, parser):
        parser.add_argument(
            "--retention-hours",
            type=int,
            default=getattr(settings, "ARRIVAL_RETENTION_HOURS", DEFAULT_ARRIVAL_RETENTION_HOURS),
            help="Hours after an activity's start before its arrival pings are cleared.",
        )

    def handle(self, *args, **opts):
        now = timezone.now()
        cutoff = now - timedelta(hours=opts["retention_hours"])
        cleared = Membership.objects.filter(
            arrived_at__isnull=False, activity__starts_at__lt=cutoff
        ).update(arrived_at=None, updated_at=now)  # .update() bypasses auto_now → set explicitly
        self.stdout.write(self.style.SUCCESS(f"arrival pings cleared: {cleared}"))
