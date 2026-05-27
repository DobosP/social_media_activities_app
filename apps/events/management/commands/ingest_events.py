from pathlib import Path

from django.core.management.base import BaseCommand, CommandError

from apps.events.services import import_events
from apps.events.sources import ICalFeedSource
from apps.places.models import Place


class Command(BaseCommand):
    help = "Ingest events from an iCalendar (.ics) feed URL or file, optionally tied to a place."

    def add_arguments(self, parser):
        parser.add_argument("--ics-url", default=None, help="URL of an iCalendar feed")
        parser.add_argument("--ics-file", default=None, help="Local .ics file path")
        parser.add_argument("--place", type=int, default=None, help="Place id to attach events to")

    def handle(self, *args, **opts):
        if not opts["ics_url"] and not opts["ics_file"]:
            raise CommandError("Provide --ics-url or --ics-file.")
        place = None
        if opts["place"]:
            place = Place.objects.filter(pk=opts["place"]).first()
            if place is None:
                raise CommandError(f"No place with id {opts['place']}.")

        if opts["ics_file"]:
            text = Path(opts["ics_file"]).read_text(encoding="utf-8")
            source = ICalFeedSource(text=text)
        else:
            source = ICalFeedSource(url=opts["ics_url"])

        count = import_events(source, place=place)
        self.stdout.write(self.style.SUCCESS(f"Imported/updated {count} event(s)."))
