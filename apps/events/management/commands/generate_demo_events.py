"""Make the events surfaces testable when the static seed has aged out (ADR-0020 §4).

The RO-EDU seed ships REAL Cluj events, but it is a snapshot: once their dates pass,
``upcoming_events()`` correctly filters them all away and every events view renders
empty. Production freshness is the daily ``sync_roedu`` job (ADR-0019 §7); THIS command
is a dev/test tool only:

- default: RESCHEDULE the seed's past events into the next four weeks, preserving each
  event's weekday and start time (so listings look realistic, multi-day spans survive);
- ``--synthesize N``: additionally create N clearly-marked demo events
  (``source="demo"``, ``external_id="demo:<n>"``) spread across public places, active
  activity types and the next three weeks, with some multi-day and some type-less
  entries — enough variety to exercise every display state cross-device.

Refuses to run outside DEBUG unless ``--force`` (demo data must never leak into prod;
re-running is idempotent for synthesis — demo external ids are upserted, not duplicated).
"""

from datetime import timedelta

from django.conf import settings
from django.core.management.base import BaseCommand, CommandError
from django.utils import timezone

from apps.events.models import Event
from apps.places.services import public_places
from apps.taxonomy.models import ActivityType


class Command(BaseCommand):
    help = "Dev-only: reschedule aged-out seed events and synthesize marked demo events."

    def add_arguments(self, parser):
        parser.add_argument("--synthesize", type=int, default=0, metavar="N")
        parser.add_argument("--force", action="store_true", help="allow outside DEBUG")
        parser.add_argument("--dry-run", action="store_true")

    def handle(self, *args, **opts):
        if not settings.DEBUG and not opts["force"]:
            raise CommandError(
                "generate_demo_events is a dev/test tool (DEBUG only). "
                "Production freshness is the daily sync_roedu job; use --force to override."
            )
        now = timezone.now()
        rescheduled = 0
        for event in Event.objects.filter(starts_at__lt=now).exclude(source="demo"):
            # Whole weeks forward keeps the weekday + local start time; land inside the
            # next 28 days so "upcoming" views fill without everything on one day.
            behind = now - event.starts_at
            weeks = (behind.days // 7) + 1
            delta = timedelta(weeks=weeks)
            if event.starts_at + delta < now:  # same-weekday edge: still behind → one more
                delta += timedelta(weeks=1)
            if opts["dry_run"]:
                rescheduled += 1
                continue
            event.starts_at += delta
            if event.ends_at is not None:
                event.ends_at += delta
            event.save(update_fields=["starts_at", "ends_at", "updated_at"])
            rescheduled += 1

        synthesized = 0
        want = opts["synthesize"]
        if want:
            places = list(public_places().order_by("pk")[:25])
            types = list(ActivityType.objects.filter(is_active=True).order_by("slug"))
            if not places:
                raise CommandError("No public places to attach demo events to — seed first.")
            for n in range(want):
                place = places[n % len(places)]
                # Every third demo event is type-less; the rest walk the taxonomy.
                activity_type = None if n % 3 == 2 else types[n % len(types)]
                starts = (now + timedelta(days=1 + (n * 2) % 21)).replace(
                    hour=10 + (n * 3) % 9, minute=0, second=0, microsecond=0
                )
                # Every fourth demo event is multi-day.
                ends = starts + (timedelta(days=2) if n % 4 == 3 else timedelta(hours=2))
                label = activity_type.name if activity_type else "Comunitate"
                if opts["dry_run"]:
                    synthesized += 1
                    continue
                _, created = Event.objects.update_or_create(
                    source="demo",
                    external_id=f"demo:{n}",
                    defaults={
                        "title": f"[DEMO] {label} la {place.display_name or place}",
                        "description": "",
                        "starts_at": starts,
                        "ends_at": ends,
                        "url": "",
                        "activity_type": activity_type,
                        "place": place,
                        "attribution": "Demo data (dev only)",
                        "license_name": "",
                        "provenance_url": "",
                    },
                )
                synthesized += 1 if created else 0

        verb = "would reschedule" if opts["dry_run"] else "rescheduled"
        self.stdout.write(
            self.style.SUCCESS(
                f"{verb} {rescheduled} past event(s); synthesized {synthesized} new demo event(s)."
            )
        )
