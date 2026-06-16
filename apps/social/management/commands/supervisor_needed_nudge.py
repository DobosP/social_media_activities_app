"""W3-F7: one calm, muteable nudge to a CHILD organiser's ACTIVE guardians when their supervised
meetup is stuck for lack of a seated supervisor.

When a CHILD-organised supervised meetup (starting within the organizer prep window) has a join
that has CLEARED the vote but can't be admitted because no supervisor is seated, the organiser's
ACTIVE guardian(s) get one in-app nudge to step in and supervise — so a child-run meetup isn't
silently stuck waiting on an adult who doesn't know they're needed.

Safety properties: the trigger re-runs the REAL vote-threshold check (join_stuck_on_supervision →
_vote_threshold_met), never a bare "a REQUESTED row exists", so a request nobody voted through never
summons a guardian. Guardian fan-out keys strictly on an ACTIVE GuardianRelationship (mirroring
mark_arrived), with blocked pairs excluded; the link goes to the /wards/ manifest because an adult
guardian is cross-cohort to the CHILD thread (linking there would leak/dead-end). Mutable kind
(F31): a guardian who turned these off gets nothing. The body carries NO waiting-joiner count or any
pressure metric (inv.2) — flat qualitative text. At-most-once per (guardian, activity) via the
(recipient, kind, url) dedup on a STABLE url, so the run_due_jobs tick can't re-nudge every run.

INERT in production until ALLOW_MINOR_ONBOARDING flips (no CHILD organisers exist otherwise).
Intended for the run_due_jobs tick.
"""

from django.core.management.base import BaseCommand
from django.urls import reverse
from django.utils import timezone

from apps.accounts.models import Cohort, GuardianRelationship
from apps.notifications.models import Notification
from apps.notifications.services import notify
from apps.safety.services import blocked_user_ids
from apps.social.models import Activity
from apps.social.services import ORGANIZER_PREP_WINDOW, join_stuck_on_supervision


class Command(BaseCommand):
    help = (
        "Nudge a CHILD organiser's guardians when their supervised meetup is stuck on supervision."
    )

    def handle(self, *args, **opts):
        now = timezone.now()
        prep_cutoff = now + ORGANIZER_PREP_WINDOW
        # Bounded: OPEN, not hidden, supervised CHILD meetups starting within the prep window. The
        # "genuinely cleared the vote but stuck on supervision" check runs per activity below.
        candidates = Activity.objects.filter(
            status=Activity.Status.OPEN,
            is_hidden=False,
            supervised=True,
            cohort=Cohort.CHILD,
            starts_at__gte=now,
            starts_at__lte=prep_cutoff,
        ).select_related("owner")
        wards_url = reverse("wards")
        sent = 0
        for activity in candidates:
            if not join_stuck_on_supervision(activity):
                continue
            owner = activity.owner
            if owner is None:
                continue
            blocked = blocked_user_ids(owner)
            # Land on the /wards/ manifest (the guardian can't read the CHILD thread); the activity
            # id keeps the dedup at-most-once per (guardian, activity) while still a STABLE url.
            url = f"{wards_url}?supervisor_needed={activity.id}"
            for rel in GuardianRelationship.objects.filter(
                ward=owner, status=GuardianRelationship.Status.ACTIVE
            ).select_related("guardian"):
                guardian = rel.guardian
                if guardian.id in blocked:
                    continue
                if Notification.objects.filter(
                    recipient=guardian,
                    kind=Notification.Kind.SUPERVISOR_NEEDED,
                    url=url,
                ).exists():
                    continue
                # notify() returns None when the guardian muted SUPERVISOR_NEEDED (F31).
                delivered = notify(
                    guardian,
                    Notification.Kind.SUPERVISOR_NEEDED,
                    title=f"“{activity.title}” needs an adult to supervise",
                    body=(
                        "A meetup your child is organising is ready to go but needs you (or "
                        "another guardian) to join as its supervisor. Open your guardian page."
                    ),
                    url=url,
                )
                if delivered:
                    sent += 1
        self.stdout.write(self.style.SUCCESS(f"supervisor-needed nudges sent: {sent}"))
