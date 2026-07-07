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

# W2-F8: fold the owner-curated, MEMBER-ONLY logistics into the reminder body so members arrive
# prepared without digging through the thread. Recipients are current MEMBERS only (the loop
# filters state=MEMBER), so these member-gated fields never reach a non-member. Each line is
# truncated and the whole body capped so a long note can't bloat the in-app / future push payload.
_REMINDER_LOGISTICS = (
    ("meeting_point", "Meet"),
    ("what_to_bring", "Bring"),
    ("first_time_note", "First time"),
)
_REMINDER_FIELD_MAX = 120
_REMINDER_BODY_MAX = 600


def _reminder_body(activity) -> str:
    """The reminder body: today's bare 'Starts {time}.' plus any present logistics as short
    labelled lines (degrades to exactly the bare line when all logistics are blank)."""
    lines = [f"Starts {activity.starts_at:%Y-%m-%d %H:%M}."]
    for field, label in _REMINDER_LOGISTICS:
        value = (getattr(activity, field, "") or "").strip()
        if not value:
            continue
        if len(value) > _REMINDER_FIELD_MAX:
            value = value[: _REMINDER_FIELD_MAX - 1].rstrip() + "…"
        lines.append(f"{label}: {value}")
    return "\n".join(lines)[:_REMINDER_BODY_MAX]


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
                # notify() returns None when the recipient muted EVENT_REMINDER — don't count
                # a notice that was never delivered.
                delivered = notify(
                    membership.user,
                    Notification.Kind.EVENT_REMINDER,
                    title=f"“{activity.title}” is starting soon",
                    body=_reminder_body(activity),
                    url=url,
                )
                if delivered:
                    sent += 1
        self.stdout.write(self.style.SUCCESS(f"reminders sent: {sent}"))
