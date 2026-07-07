"""Dev-only demo accounts so logged-in surfaces are testable (ADR-0020 §4 spirit).

Creates (idempotently) a small cast with REAL assurance records via the same
``apply_assurance`` path the EUDI flow uses, plus enough social fabric to make the
logged-in views non-empty: two adults connected to each other, a shared activity with a
thread, and a staff/superuser for /admin. DEBUG-guarded; fixed throwaway passwords are
printed on completion — never use outside a dev box.
"""

from datetime import timedelta

from django.conf import settings
from django.contrib.auth import get_user_model
from django.core.management.base import BaseCommand, CommandError
from django.utils import timezone

PASSWORD = "parola-demo-1"


class Command(BaseCommand):
    help = "Dev-only: seed demo users (ana/dan/staff) + a joined activity for logged-in testing."

    def add_arguments(self, parser):
        parser.add_argument("--force", action="store_true", help="allow outside DEBUG")

    def handle(self, *args, **opts):
        if not settings.DEBUG and not opts["force"]:
            raise CommandError(
                "seed_demo_users is a dev tool (DEBUG only); use --force to override."
            )
        from apps.accounts.identity.base import AssuranceResult
        from apps.accounts.models import AgeBand
        from apps.accounts.services import apply_assurance
        from apps.places.services import public_places
        from apps.social.models import Activity, Membership
        from apps.social.services import create_activity
        from apps.taxonomy.models import ActivityType

        User = get_user_model()

        def adult(username, display):
            user, created = User.objects.get_or_create(
                username=username, defaults={"display_name": display}
            )
            if created:
                user.set_password(PASSWORD)
                user.save()
                apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
            return user, created

        ana, ana_new = adult("ana.demo", "Ana Demo")
        dan, dan_new = adult("dan.demo", "Dan Demo")

        staff, staff_new = User.objects.get_or_create(
            username="staff.demo",
            defaults={"display_name": "Staff Demo", "is_staff": True, "is_superuser": True},
        )
        if staff_new:
            staff.set_password(PASSWORD)
            staff.save()
            apply_assurance(staff, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))

        made_activity = False
        if not Activity.objects.filter(owner=ana, title__startswith="[DEMO]").exists():
            place = public_places().filter(place_activities__isnull=False).first()
            atype = ActivityType.objects.filter(is_active=True).order_by("slug").first()
            if place is not None and atype is not None:
                activity = create_activity(
                    ana,
                    place=place,
                    activity_type=atype,
                    title=f"[DEMO] {atype.name} cu Ana",
                    description="Meetup demo pentru testarea vederilor autentificate.",
                    starts_at=(timezone.now() + timedelta(days=2)).replace(
                        hour=18, minute=0, second=0, microsecond=0
                    ),
                    beginners_welcome=True,
                )
                Membership.objects.get_or_create(
                    activity=activity,
                    user=dan,
                    defaults={
                        "role": Membership.Role.MEMBER,
                        "state": Membership.State.MEMBER,
                        "decided_at": timezone.now(),
                    },
                )
                made_activity = True

        self.stdout.write(
            self.style.SUCCESS(
                "demo users ready: ana.demo / dan.demo / staff.demo (admin) — password: "
                f"{PASSWORD}. New: ana={ana_new} dan={dan_new} staff={staff_new} "
                f"activity_created={made_activity}"
            )
        )
