"""Resolve venue cover images from Wikimedia Commons (ADR-0019 §2).

Walks public places that reference Commons/Wikidata in their OSM ``raw_tags``
(``wikimedia_commons``, ``image`` pointing at Commons, or ``wikidata`` → P18) and
caches ONE licensed thumbnail per place in our object storage, with attribution.
Idempotent: places with a cover are skipped; places checked once are marked
(``raw_tags.cover_checked``) so periodic re-runs stay cheap. Never touches Google
imagery (ToS forbids caching) and never scrapes — Commons metadata only.

    python manage.py resolve_place_covers --city "Cluj-Napoca" --limit 50 --dry-run
"""

from django.core.management.base import BaseCommand

from apps.places.enrichment.commons import CHECKED_MARKER, CommonsCoverResolver, commons_file_title
from apps.places.models import Place
from apps.places.services import public_places


class Command(BaseCommand):
    help = "Cache Wikimedia Commons cover images for public places (facts + free licenses only)."

    def add_arguments(self, parser):
        parser.add_argument("--city", default=None)
        parser.add_argument("--limit", type=int, default=200)
        parser.add_argument("--dry-run", action="store_true")
        parser.add_argument(
            "--recheck",
            action="store_true",
            help="also revisit places already marked cover_checked",
        )

    def handle(self, *args, **opts):
        qs = public_places(Place.objects.select_related("cover").order_by("pk"))
        if opts["city"]:
            qs = qs.filter(address_city__iexact=opts["city"])
        resolver = CommonsCoverResolver()
        seen = resolved = skipped_checked = no_candidate = 0
        for place in qs.iterator():
            if seen >= opts["limit"]:
                break
            if getattr(place, "cover", None) and place.cover.storage_key:
                continue
            tags = place.raw_tags or {}
            if tags.get(CHECKED_MARKER) and not opts["recheck"]:
                skipped_checked += 1
                continue
            has_candidate = bool(commons_file_title(place) or isinstance(tags.get("wikidata"), str))
            if not has_candidate:
                no_candidate += 1
                continue
            seen += 1
            if opts["dry_run"]:
                self.stdout.write(f"would resolve: {place.pk} {place.display_name}")
                continue
            cover = resolver.resolve(place)
            if cover is not None:
                resolved += 1
            place.raw_tags = {**tags, CHECKED_MARKER: True}
            place.save(update_fields=["raw_tags"])

        verb = "would resolve" if opts["dry_run"] else "resolved"
        self.stdout.write(
            f"{verb} {resolved if not opts['dry_run'] else seen} cover(s) "
            f"(candidates seen: {seen}, already-checked skipped: {skipped_checked}, "
            f"no Commons/Wikidata reference: {no_candidate})"
        )
