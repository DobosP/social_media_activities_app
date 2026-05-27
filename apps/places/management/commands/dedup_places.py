import logging
from collections import Counter

from django.core.management.base import BaseCommand
from django.db import transaction

from apps.places.enrichment.dedup import (
    DEFAULT_MAX_DISTANCE_M,
    DEFAULT_MIN_NAME_RATIO,
    find_duplicate,
    merge_places,
)
from apps.places.models import Place

logger = logging.getLogger(__name__)

# Source preference for choosing the canonical record (earlier = preferred).
CANONICAL_PRIORITY = ["osm", "overture", "google", "user"]


class Command(BaseCommand):
    help = (
        "Detect and merge cross-source duplicate places (close + similar name). "
        "Dry-run by default; pass --apply to actually merge."
    )

    def add_arguments(self, parser):
        parser.add_argument("--apply", action="store_true", help="Perform merges (default: report)")
        parser.add_argument("--city", default=None, help="Limit to this address_city")
        parser.add_argument("--max-distance-m", type=float, default=DEFAULT_MAX_DISTANCE_M)
        parser.add_argument("--min-name-ratio", type=float, default=DEFAULT_MIN_NAME_RATIO)

    def handle(self, *args, **opts):
        qs = Place.objects.all().order_by("id")
        if opts["city"]:
            qs = qs.filter(address_city__iexact=opts["city"])

        counts: Counter = Counter()
        seen_deleted: set[int] = set()

        # Walk non-preferred sources first so they fold into the preferred canonical.
        for place in sorted(qs, key=self._sort_key, reverse=True):
            if place.pk in seen_deleted:
                continue
            match = find_duplicate(
                place.location,
                place.name,
                exclude_pk=place.pk,
                max_distance_m=opts["max_distance_m"],
                min_name_ratio=opts["min_name_ratio"],
            )
            if match is None or match.pk in seen_deleted:
                continue
            canonical, duplicate = self._canonical_pair(match, place)
            counts["pairs"] += 1
            self.stdout.write(
                f"  {'merge' if opts['apply'] else 'would merge'}: "
                f"#{duplicate.pk}[{duplicate.source}] '{duplicate.name}' "
                f"-> #{canonical.pk}[{canonical.source}] '{canonical.name}'"
            )
            if opts["apply"]:
                with transaction.atomic():
                    merge_places(canonical, duplicate)
                seen_deleted.add(duplicate.pk)
                counts["merged"] += 1

        self.stdout.write(self.style.MIGRATE_HEADING("Dedup summary"))
        verb = "merged" if opts["apply"] else "candidate pairs"
        self.stdout.write(f"  {verb}: {counts['merged'] if opts['apply'] else counts['pairs']}")

    @staticmethod
    def _sort_key(place) -> int:
        try:
            return CANONICAL_PRIORITY.index(place.source)
        except ValueError:
            return len(CANONICAL_PRIORITY)

    def _canonical_pair(self, a, b):
        """Return (canonical, duplicate) by source preference, then by lower pk."""
        ka, kb = self._sort_key(a), self._sort_key(b)
        if ka != kb:
            return (a, b) if ka < kb else (b, a)
        return (a, b) if a.pk <= b.pk else (b, a)
