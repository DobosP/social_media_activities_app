import logging
from collections import Counter

from django.core.management.base import BaseCommand

from apps.places.enrichment.google import GooglePlacesEnricher
from apps.places.enrichment.opening_hours import parse_opening_hours
from apps.places.models import Place

logger = logging.getLogger(__name__)


class Command(BaseCommand):
    help = (
        "Enrich existing places: (re)parse opening_hours into structured JSON, and "
        "optionally pull Google Places live status/links (only when enabled)."
    )

    def add_arguments(self, parser):
        parser.add_argument("--city", default=None, help="Limit to this address_city")
        parser.add_argument("--source", default=None, help="Limit to this source")
        parser.add_argument("--limit", type=int, default=None)
        parser.add_argument("--dry-run", action="store_true")
        parser.add_argument(
            "--google",
            action="store_true",
            help="Also enrich via Google Places (requires GOOGLE_PLACES_ENABLED + key).",
        )

    def handle(self, *args, **opts):
        qs = Place.objects.all().order_by("id")
        if opts["city"]:
            qs = qs.filter(address_city__iexact=opts["city"])
        if opts["source"]:
            qs = qs.filter(source=opts["source"])
        if opts["limit"]:
            qs = qs[: opts["limit"]]

        counts: Counter = Counter()
        enricher = GooglePlacesEnricher() if opts["google"] else None
        if opts["google"] and (enricher is None or not enricher.enabled):
            self.stdout.write(
                self.style.WARNING("Google enrichment requested but disabled; skipping it.")
            )
            enricher = None

        for place in qs:
            self._parse_hours(place, counts, dry_run=opts["dry_run"])
            if enricher is not None and not opts["dry_run"]:
                self._google(place, enricher, counts)

        self._report(counts, opts["dry_run"])

    def _parse_hours(self, place, counts, *, dry_run):
        if not place.opening_hours_raw:
            return
        parsed = parse_opening_hours(place.opening_hours_raw)
        counts["hours_parsed" if parsed else "hours_unparsed"] += 1
        if parsed != place.opening_hours and not dry_run:
            place.opening_hours = parsed
            place.save(update_fields=["opening_hours"])
            counts["hours_updated"] += 1

    def _google(self, place, enricher, counts):
        try:
            status = enricher.enrich_place(place)
        except Exception as exc:  # external API: log and continue
            logger.warning("Google enrichment failed for place %s: %s", place.pk, exc)
            counts["google_error"] += 1
            return
        if status is None:
            counts["google_unresolved"] += 1
        else:
            counts["google_enriched"] += 1

    def _report(self, counts, dry_run):
        self.stdout.write(self.style.MIGRATE_HEADING("Enrichment summary"))
        prefix = "DRY RUN — " if dry_run else ""
        self.stdout.write(
            f"  {prefix}opening_hours: parsed={counts['hours_parsed']} "
            f"unparsed={counts['hours_unparsed']} updated={counts['hours_updated']}"
        )
        if counts["google_enriched"] or counts["google_unresolved"] or counts["google_error"]:
            self.stdout.write(
                f"  google: enriched={counts['google_enriched']} "
                f"unresolved={counts['google_unresolved']} errors={counts['google_error']}"
            )
