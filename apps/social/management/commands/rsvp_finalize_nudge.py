"""W2-F11: one quiet "still coming?" RSVP nudge per (member, activity).

Sent only inside the arrival window, to members whose RSVP is still UNKNOWN, so the group gets an
honest last-minute headcount — without shaming or repeated pestering. At-most-once per
(recipient, RSVP_NUDGE, url) via the same dedup as send_activity_reminders; mutable (F31), so a
member who turned RSVP nudges off gets nothing. The intent stays transient (resets on leave), so
nothing is ever aggregated into a per-user reliability history. Intended for the run_due_jobs tick.
"""

from datetime import timedelta

from django.conf import settings
from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.notifications.models import Notification
from apps.notifications.services import notify
from apps.social.models import Activity, Membership
from apps.social.services import (
    ARRIVAL_WINDOW_AFTER_HOURS,
    ARRIVAL_WINDOW_BEFORE_HOURS,
    arrival_window_open,
    voting_members,
)


class Command(BaseCommand):
    help = "Send a one-shot 'still coming?' RSVP nudge to undecided members in the arrival window."

    def handle(self, *args, **opts):
        now = timezone.now()
        before = getattr(settings, "ARRIVAL_WINDOW_BEFORE_HOURS", ARRIVAL_WINDOW_BEFORE_HOURS)
        after = getattr(settings, "ARRIVAL_WINDOW_AFTER_HOURS", ARRIVAL_WINDOW_AFTER_HOURS)
        # Bounded query: only activities whose start is near now can be in the window. The exact
        # gate is arrival_window_open() below (single source of truth); this is just a pre-filter.
        candidates = Activity.objects.filter(
            status=Activity.Status.OPEN,
            is_hidden=False,
            starts_at__gte=now - timedelta(hours=after + 1),
            starts_at__lte=now + timedelta(hours=before + 1),
        )
        sent = 0
        for activity in candidates:
            if not arrival_window_open(activity):
                continue
            # The WEB detail page (where the RSVP control renders) — NOT the API endpoint.
            url = f"/activities/{activity.id}/"
            # voting_members excludes seated supervisory GUARDIANs (they don't RSVP); only members
            # whose go/no-go is still UNKNOWN are nudged.
            undecided = (
                voting_members(activity)
                .filter(attendance_intent=Membership.AttendanceIntent.UNKNOWN)
                .select_related("user")
            )
            for membership in undecided:
                already = Notification.objects.filter(
                    recipient=membership.user,
                    kind=Notification.Kind.RSVP_NUDGE,
                    url=url,
                ).exists()
                if already:
                    continue
                # notify() returns None when the member muted RSVP_NUDGE (F31) — don't count it.
                delivered = notify(
                    membership.user,
                    Notification.Kind.RSVP_NUDGE,
                    title=f"Still coming to “{activity.title}”?",
                    body="A quick yes or no helps everyone plan — you can update it any time.",
                    url=url,
                )
                if delivered:
                    sent += 1
        self.stdout.write(self.style.SUCCESS(f"rsvp nudges sent: {sent}"))
