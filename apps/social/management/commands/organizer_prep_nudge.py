"""W3-F6: one calm, muteable prep-gap nudge to a meetup's organisers.

When an OPEN meetup starts within the organizer prep window (48h) and still has no meeting
point, its owner and co-organisers get a single in-app nudge — so members never travel to a
meetup with nowhere to gather, and the volunteer isn't shamed, just gently prompted once.

Self-scoped to organisers (owner + co-organisers; co-orgs are ADULT-only by construction, so
this is never a minor fan-out, and a regular member is never nudged). Mutable (F31): an
organiser who turned prep nudges off gets nothing. At-most-once per
(organiser, ORGANIZER_PREP, /activities/{id}/) via the same dedup as send_activity_reminders —
the url is STABLE (no timestamp/window), so the run_due_jobs tick can't re-nudge every run.
Intended for the run_due_jobs tick.
"""

from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.notifications.models import Notification
from apps.notifications.services import notify
from apps.social.models import Activity, Membership
from apps.social.services import ORGANIZER_PREP_WINDOW


class Command(BaseCommand):
    help = "Nudge a meetup's organisers once when it starts soon and still has no meeting point."

    def handle(self, *args, **opts):
        now = timezone.now()
        prep_cutoff = now + ORGANIZER_PREP_WINDOW
        # Bounded query: OPEN, not hidden, starting between now and the prep cutoff. The blank
        # meeting_point gate runs in Python below (mirrors organizer_console's
        # missing_meeting_point predicate exactly, so a whitespace-only value still counts as
        # blank). Memberships are prefetched so the per-activity organiser lookup stays O(1).
        candidates = (
            Activity.objects.filter(
                status=Activity.Status.OPEN,
                is_hidden=False,
                starts_at__gte=now,
                starts_at__lte=prep_cutoff,
            )
            .select_related("owner")
            .prefetch_related("memberships__user")
        )
        sent = 0
        for activity in candidates:
            if (activity.meeting_point or "").strip():
                continue  # a meeting point is set — nothing to prompt
            # The WEB detail page (the edit target) — a STABLE url so the at-most-once
            # (recipient, kind, url) guard holds across every run_due_jobs tick.
            url = f"/activities/{activity.id}/"
            for organizer in self._organizers(activity):
                already = Notification.objects.filter(
                    recipient=organizer,
                    kind=Notification.Kind.ORGANIZER_PREP,
                    url=url,
                ).exists()
                if already:
                    continue
                # notify() returns None when the organiser muted ORGANIZER_PREP (F31).
                delivered = notify(
                    organizer,
                    Notification.Kind.ORGANIZER_PREP,
                    title=f"“{activity.title}” still has no meeting point",
                    body=(
                        "It starts soon and members won't know where to gather. Add a "
                        "meeting point so everyone can find the group."
                    ),
                    url=url,
                )
                if delivered:
                    sent += 1
        self.stdout.write(self.style.SUCCESS(f"organizer prep nudges sent: {sent}"))

    @staticmethod
    def _organizers(activity):
        """Yield the owner then current co-organisers (deduped by id). Co-organisers are
        ADULT-only by construction (grant_co_organizer), so this is never a minor fan-out;
        a regular MEMBER is never yielded. Reads the prefetched memberships, so no extra
        per-activity query."""
        seen = set()
        owner = activity.owner
        if owner is not None:
            seen.add(owner.id)
            yield owner
        for m in activity.memberships.all():
            if (
                m.role == Membership.Role.CO_ORGANIZER
                and m.state == Membership.State.MEMBER
                and m.user_id not in seen
            ):
                seen.add(m.user_id)
                yield m.user
