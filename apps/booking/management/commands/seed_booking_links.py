from django.core.management.base import BaseCommand

from apps.booking.models import PlaceBookingInfo
from apps.places.models import Place


class Command(BaseCommand):
    help = (
        "Create deep-link booking info for collected places that have a website but no "
        "booking_info yet — so reservation-capable venues are bookable out of the box."
    )

    def add_arguments(self, parser):
        parser.add_argument("--dry-run", action="store_true")

    def handle(self, *args, **options):
        candidates = Place.objects.exclude(website="").filter(booking_info__isnull=True)
        created = 0
        for place in candidates.iterator():
            if options["dry_run"]:
                created += 1
                continue
            PlaceBookingInfo.objects.create(
                place=place,
                provider="deeplink",
                deep_link=place.website,
                instructions="Reserve via the venue's website.",
            )
            created += 1
        verb = "Would create" if options["dry_run"] else "Created"
        self.stdout.write(self.style.SUCCESS(f"{verb} {created} booking link(s) from websites."))
