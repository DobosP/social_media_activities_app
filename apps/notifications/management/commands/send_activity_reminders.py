"""Notify current members of activities starting within a lookahead window.

Idempotent per (member, activity): an event-reminder is sent at most once for the
same activity URL. Intended for a scheduled job (cron) close to event time.
"""

from datetime import timedelta

from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.notifications.models import Notification
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
            status=Activity.Status.OPEN,
            is_hidden=False,  # don't re-surface a moderator-removed activity's title
            starts_at__gte=now,
            starts_at__lte=horizon,
        )
        sent = 0
        for activity in upcoming:
            url = f"/api/social/activities/{activity.id}/"
            for membership in activity.memberships.filter(
                state=Membership.State.MEMBER
            ).select_related("user"):
                already = Notification.objects.filter(
                    recipient=membership.user,
                    kind=Notification.Kind.EVENT_REMINDER,
                    url=url,
                ).exists()
                if already:
                    continue
                notify(
                    membership.user,
                    Notification.Kind.EVENT_REMINDER,
                    title=f"“{activity.title}” is starting soon",
                    body=f"Starts {activity.starts_at:%Y-%m-%d %H:%M}.",
                    url=url,
                )
                sent += 1
        self.stdout.write(self.style.SUCCESS(f"reminders sent: {sent}"))
