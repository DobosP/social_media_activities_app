"""Send "starting soon" reminders to members of upcoming activities.

Reads social activities read-only and notifies each current member (gated by their
opt-in preferences). Idempotent per (member, activity): a reminder is sent at most
once. Intended to run on a schedule (cron) close to the event window.
"""

from datetime import timedelta

from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.notifications.models import Notification, NotificationType
from apps.notifications.services import notify
from apps.social.models import Activity, Membership


class Command(BaseCommand):
    help = "Notify members of activities starting within the lookahead window."

    def add_arguments(self, parser):
        parser.add_argument("--within-hours", type=int, default=24)

    def handle(self, *args, **opts):
        now = timezone.now()
        horizon = now + timedelta(hours=opts["within_hours"])
        upcoming = Activity.objects.filter(
            status=Activity.Status.OPEN, starts_at__gte=now, starts_at__lte=horizon
        )
        sent = 0
        for activity in upcoming:
            members = activity.memberships.filter(state=Membership.State.MEMBER)
            for membership in members.select_related("user"):
                already = Notification.objects.filter(
                    recipient=membership.user,
                    ntype=NotificationType.EVENT_REMINDER,
                    data__activity_id=activity.id,
                ).exists()
                if already:
                    continue
                result = notify(
                    membership.user,
                    NotificationType.EVENT_REMINDER,
                    title=f"'{activity.title}' is starting soon",
                    body=f"Starts {activity.starts_at:%Y-%m-%d %H:%M}.",
                    data={"activity_id": activity.id, "kind": "reminder"},
                )
                if result is not None:
                    sent += 1
        self.stdout.write(self.style.SUCCESS(f"reminders sent: {sent}"))
