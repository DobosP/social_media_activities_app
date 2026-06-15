"""Clear stale arrival pings (F3) + transit cues (W2-F9) so neither becomes a presence record.

An arrived_at timestamp / transit_status is wiped once the activity's start is more than
--retention-hours in the past — long enough that the cue is useful at meetup time, short enough
that it is not a durable "who was where" log. Idempotent: only rows still carrying a cue are
touched, and the bulk update never re-pings anyone. Intended for the shared run_due_jobs cron tick.
"""

from datetime import timedelta

from django.conf import settings
from django.core.management.base import BaseCommand
from django.db.models import Q
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
        # A transit cue can outlive its arrival (a member said "on my way" but never tapped
        # arrived), so clear either signal that is still set on a long-past meetup.
        cleared = (
            Membership.objects.filter(activity__starts_at__lt=cutoff)
            .filter(Q(arrived_at__isnull=False) | ~Q(transit_status=Membership.TransitStatus.NONE))
            .update(  # .update() bypasses auto_now → set updated_at explicitly
                arrived_at=None,
                transit_status=Membership.TransitStatus.NONE,
                updated_at=now,
            )
        )
        self.stdout.write(self.style.SUCCESS(f"arrival/transit cues cleared: {cleared}"))
