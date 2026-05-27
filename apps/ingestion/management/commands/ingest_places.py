import logging
from collections import Counter

from django.conf import settings
from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand, CommandError
from django.db import transaction
from django.utils import timezone

from apps.ingestion.mapping import match_element
from apps.ingestion.sources.base import SourceAdapter
from apps.ingestion.sources.overpass import OverpassAdapter
from apps.ingestion.sources.overture import OvertureAdapter
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityType

logger = logging.getLogger(__name__)

PROTECTED_ORIGINS = {PlaceActivity.Origin.CONFIRMED, PlaceActivity.Origin.MANUAL}
UNMAPPED_TAG_KEYS = ("leisure", "amenity", "shop", "sport")


class Command(BaseCommand):
    help = "Ingest places from a data source (OSM/Overpass) into the knowledge graph."

    def add_arguments(self, parser):
        parser.add_argument("--source", default="osm", choices=["osm", "overture"])
        parser.add_argument("--city", default=None, help="Administrative area name")
        parser.add_argument("--bbox", default=None, help="minlon,minlat,maxlon,maxlat")
        parser.add_argument("--dry-run", action="store_true")
        parser.add_argument("--limit", type=int, default=None)
        parser.add_argument("--overpass-url", default=None)
        parser.add_argument("--min-confidence", type=float, default=0.0)

    def _build_adapter(self, source: str, overpass_url: str | None) -> SourceAdapter:
        if source == "osm":
            return OverpassAdapter(
                endpoint=overpass_url or settings.OVERPASS_URL,
                user_agent=settings.INGEST_USER_AGENT,
            )
        if source == "overture":
            return OvertureAdapter()
        raise CommandError(f"Unknown source: {source}")

    def _resolve_area(self, opts) -> tuple[str | None, tuple | None]:
        if opts["bbox"]:
            try:
                bbox = tuple(float(x) for x in opts["bbox"].split(","))
            except ValueError as exc:
                raise CommandError("--bbox must be 'minlon,minlat,maxlon,maxlat'") from exc
            if len(bbox) != 4:
                raise CommandError("--bbox must have 4 comma-separated numbers")
            return None, bbox
        return opts["city"] or settings.INGEST_DEFAULT_CITY, None

    def handle(self, *args, **opts):
        adapter = self._build_adapter(opts["source"], opts["overpass_url"])
        city, bbox = self._resolve_area(opts)
        dry_run, limit, min_conf = opts["dry_run"], opts["limit"], opts["min_confidence"]

        # Cache slug -> ActivityType once; fail loudly if the taxonomy is unseeded.
        types = {t.slug: t for t in ActivityType.objects.all()}
        if not types:
            raise CommandError("No ActivityType rows found. Run migrations (seed) first.")

        counts: Counter = Counter()
        unmapped: Counter = Counter()

        for raw in adapter.fetch(city=city, bbox=bbox, limit=limit):
            matches = [m for m in match_element(raw.tags) if m[2] >= min_conf]
            if not matches:
                signature = ",".join(
                    f"{k}={raw.tags[k]}" for k in UNMAPPED_TAG_KEYS if k in raw.tags
                )
                unmapped[signature or "(other)"] += 1

            if dry_run:
                counts["seen"] += 1
                if matches:
                    counts["would_map"] += 1
                continue

            self._upsert(raw, matches, types, counts)

        self._report(dry_run, counts, unmapped)

    def _upsert(self, raw, matches, types, counts):
        with transaction.atomic():
            place, created = Place.objects.update_or_create(
                source="osm",
                osm_type=raw.osm_type,
                osm_id=raw.osm_id,
                defaults={
                    "name": raw.name,
                    "location": Point(raw.lon, raw.lat, srid=4326),
                    "address_street": raw.address.get("street", ""),
                    "address_housenumber": raw.address.get("housenumber", ""),
                    "address_city": raw.address.get("city", ""),
                    "address_postcode": raw.address.get("postcode", ""),
                    "address_country": raw.address.get("country", ""),
                    "opening_hours_raw": raw.opening_hours_raw,
                    "raw_tags": raw.tags,
                    "last_seen_at": timezone.now(),
                },
            )
            counts["place_created" if created else "place_updated"] += 1

            for slug, rule_id, confidence in matches:
                activity = types.get(slug)
                if activity is None:
                    raise CommandError(f"Mapping references unknown activity slug: {slug}")
                existing = (
                    PlaceActivity.objects.filter(place=place, activity=activity)
                    .only("id", "origin")
                    .first()
                )
                if existing and existing.origin in PROTECTED_ORIGINS:
                    continue  # never clobber user-confirmed / manual edges
                _, edge_created = PlaceActivity.objects.update_or_create(
                    place=place,
                    activity=activity,
                    defaults={
                        "origin": PlaceActivity.Origin.INFERRED,
                        "confidence": confidence,
                        "source": "osm",
                        "mapping_rule": rule_id,
                    },
                )
                counts["edge_created" if edge_created else "edge_updated"] += 1

    def _report(self, dry_run, counts, unmapped):
        self.stdout.write(self.style.MIGRATE_HEADING("Ingestion summary"))
        if dry_run:
            self.stdout.write(f"  DRY RUN — seen={counts['seen']} would_map={counts['would_map']}")
        else:
            self.stdout.write(
                f"  places: created={counts['place_created']} updated={counts['place_updated']}"
            )
            self.stdout.write(
                f"  edges:  created={counts['edge_created']} updated={counts['edge_updated']}"
            )
        if unmapped:
            top = ", ".join(f"{sig}×{n}" for sig, n in unmapped.most_common(8))
            self.stdout.write(f"  unmapped tag-sets ({sum(unmapped.values())} places): {top}")
