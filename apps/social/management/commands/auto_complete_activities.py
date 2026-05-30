"""Auto-complete activities whose time has comfortably passed.

An OPEN activity is moved to its terminal COMPLETED state once its end — or its start,
when no end was given — is more than ``--grace-hours`` in the past. This stops a finished
(or owner-abandoned) meetup from lingering in "your activities" and other status-agnostic
lists as though it were still live. Idempotent: only OPEN activities are ever touched, and
the bulk update never re-completes a CANCELLED/COMPLETED one. Intended for the shared
``run_due_jobs`` cron tick.
"""

from datetime import timedelta

from django.core.management.base import BaseCommand
from django.db.models import Q
from django.utils import timezone

from apps.social.models import Activity

# Conservative default so an activity is never completed while it might still be happening
# (no precise end time is required on an Activity).
DEFAULT_GRACE_HOURS = 12


class Command(BaseCommand):
    help = "Move past OPEN activities to COMPLETED (after a grace window)."

    def add_arguments(self, parser):
        parser.add_argument(
            "--grace-hours",
            type=int,
            default=DEFAULT_GRACE_HOURS,
            help="Hours after an activity's end/start before it is auto-completed.",
        )

    def handle(self, *args, **opts):
        now = timezone.now()
        cutoff = now - timedelta(hours=opts["grace_hours"])
        # effective end = ends_at if set, else starts_at. COMPLETE when that is before cutoff.
        completed = (
            Activity.objects.filter(status=Activity.Status.OPEN)
            .filter(
                Q(ends_at__isnull=False, ends_at__lt=cutoff)
                | Q(ends_at__isnull=True, starts_at__lt=cutoff)
            )
            # auto_now would be bypassed by .update(); set updated_at explicitly.
            .update(status=Activity.Status.COMPLETED, updated_at=now)
        )
        self.stdout.write(self.style.SUCCESS(f"activities completed: {completed}"))
