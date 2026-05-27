import logging
from collections import Counter

from django.conf import settings
from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand, CommandError
from django.db import transaction
from django.utils import timezone

from apps.ingestion.mapping import match_element
from apps.ingestion.sources.base import RawPlace, SourceAdapter
from apps.ingestion.sources.overpass import OverpassAdapter
from apps.ingestion.sources.overture import OvertureAdapter, match_overture
from apps.places.enrichment.dedup import find_duplicate
from apps.places.enrichment.opening_hours import parse_opening_hours
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityType

logger = logging.getLogger(__name__)

PROTECTED_ORIGINS = {PlaceActivity.Origin.CONFIRMED, PlaceActivity.Origin.MANUAL}
UNMAPPED_TAG_KEYS = ("leisure", "amenity", "shop", "sport")
PRIMARY_SOURCE = "osm"


class Command(BaseCommand):
    help = "Ingest places from a data source (OSM/Overpass or Overture) into the knowledge graph."

    def add_arguments(self, parser):
        parser.add_argument("--source", default="osm", choices=["osm", "overture"])
        parser.add_argument("--city", default=None, help="Administrative area name (OSM only)")
        parser.add_argument("--bbox", default=None, help="minlon,minlat,maxlon,maxlat")
        parser.add_argument("--dry-run", action="store_true")
        parser.add_argument("--limit", type=int, default=None)
        parser.add_argument("--overpass-url", default=None)
        parser.add_argument(
            "--overture-path",
            default=None,
            help="Parquet path/glob for Overture places (defaults to OVERTURE_DATA_PATH)",
        )
        parser.add_argument(
            "--no-dedup",
            dest="dedup",
            action="store_false",
            help="Disable cross-source de-duplication for secondary sources.",
        )
        parser.set_defaults(dedup=True)
        parser.add_argument("--min-confidence", type=float, default=0.0)

    def _build_adapter(self, source: str, opts) -> SourceAdapter:
        if source == "osm":
            return OverpassAdapter(
                endpoint=opts["overpass_url"] or settings.OVERPASS_URL,
                user_agent=settings.INGEST_USER_AGENT,
            )
        if source == "overture":
            data_path = opts["overture_path"] or getattr(settings, "OVERTURE_DATA_PATH", "")
            if not data_path:
                raise CommandError(
                    "Overture needs --overture-path or OVERTURE_DATA_PATH (parquet path/glob)."
                )
            return OvertureAdapter(data_path=data_path)
        raise CommandError(f"Unknown source: {source}")

    def _resolve_area(self, source, opts) -> tuple[str | None, tuple | None]:
        if opts["bbox"]:
            try:
                bbox = tuple(float(x) for x in opts["bbox"].split(","))
            except ValueError as exc:
                raise CommandError("--bbox must be 'minlon,minlat,maxlon,maxlat'") from exc
            if len(bbox) != 4:
                raise CommandError("--bbox must have 4 comma-separated numbers")
            return None, bbox
        if source == "overture":
            raise CommandError("Overture requires --bbox (it has no admin-area index).")
        return opts["city"] or settings.INGEST_DEFAULT_CITY, None

    @staticmethod
    def _match(raw: RawPlace) -> list[tuple[str, str, float]]:
        if raw.source == "overture":
            return match_overture(
                raw.tags.get("overture:category"), raw.tags.get("overture:alternate")
            )
        return match_element(raw.tags)

    def handle(self, *args, **opts):
        source = opts["source"]
        adapter = self._build_adapter(source, opts)
        city, bbox = self._resolve_area(source, opts)
        dry_run, limit, min_conf = opts["dry_run"], opts["limit"], opts["min_confidence"]
        dedup = opts["dedup"]

        types = {t.slug: t for t in ActivityType.objects.all()}
        if not types:
            raise CommandError("No ActivityType rows found. Run migrations (seed) first.")

        counts: Counter = Counter()
        unmapped: Counter = Counter()

        for raw in adapter.fetch(city=city, bbox=bbox, limit=limit):
            matches = [m for m in self._match(raw) if m[2] >= min_conf]
            if not matches:
                unmapped[self._unmapped_signature(raw)] += 1

            if dry_run:
                counts["seen"] += 1
                if matches:
                    counts["would_map"] += 1
                continue

            self._upsert(raw, matches, types, counts, dedup=dedup)

        self._report(dry_run, counts, unmapped)

    @staticmethod
    def _unmapped_signature(raw: RawPlace) -> str:
        if raw.source == "overture":
            return f"category={raw.tags.get('overture:category')}" or "(other)"
        signature = ",".join(f"{k}={raw.tags[k]}" for k in UNMAPPED_TAG_KEYS if k in raw.tags)
        return signature or "(other)"

    def _upsert(self, raw, matches, types, counts, *, dedup):
        with transaction.atomic():
            point = Point(raw.lon, raw.lat, srid=4326)
            parsed_hours = parse_opening_hours(raw.opening_hours_raw)
            defaults = {
                "name": raw.name,
                "location": point,
                "address_street": raw.address.get("street", ""),
                "address_housenumber": raw.address.get("housenumber", ""),
                "address_city": raw.address.get("city", ""),
                "address_postcode": raw.address.get("postcode", ""),
                "address_country": raw.address.get("country", ""),
                "opening_hours_raw": raw.opening_hours_raw,
                "opening_hours": parsed_hours,
                "raw_tags": raw.tags,
                "last_seen_at": timezone.now(),
            }

            place = self._resolve_place(raw, point, defaults, counts, dedup=dedup)

            for slug, rule_id, confidence in matches:
                activity = types.get(slug)
                if activity is None:
                    raise CommandError(f"Mapping references unknown activity slug: {slug}")
                self._upsert_edge(place, activity, raw.source, rule_id, confidence, counts)

    def _resolve_place(self, raw, point, defaults, counts, *, dedup):
        if raw.source == PRIMARY_SOURCE:
            place, created = Place.objects.update_or_create(
                source=PRIMARY_SOURCE,
                osm_type=raw.osm_type,
                osm_id=raw.osm_id,
                defaults=defaults,
            )
            counts["place_created" if created else "place_updated"] += 1
            return place

        # Secondary source: fold into an existing place if it looks like the same
        # venue (cross-source dedup); otherwise upsert keyed by (source, external_id).
        if dedup:
            canonical = find_duplicate(point, raw.name, exclude_source=raw.source)
            if canonical is not None:
                self._record_merged_source(canonical, raw, defaults)
                counts["deduped"] += 1
                return canonical

        place, created = Place.objects.update_or_create(
            source=raw.source,
            external_id=raw.external_id,
            defaults=defaults,
        )
        counts["place_created" if created else "place_updated"] += 1
        return place

    @staticmethod
    def _record_merged_source(canonical, raw, defaults):
        merged = canonical.raw_tags.get("merged_sources", []) if canonical.raw_tags else []
        entry = {"source": raw.source, "external_id": raw.external_id}
        if entry not in merged:
            merged.append(entry)
        canonical.raw_tags = {**(canonical.raw_tags or {}), "merged_sources": merged}
        update_fields = ["raw_tags", "last_seen_at"]
        canonical.last_seen_at = defaults["last_seen_at"]
        if not canonical.opening_hours_raw and raw.opening_hours_raw:
            canonical.opening_hours_raw = raw.opening_hours_raw
            canonical.opening_hours = defaults["opening_hours"]
            update_fields += ["opening_hours_raw", "opening_hours"]
        canonical.save(update_fields=update_fields)

    @staticmethod
    def _upsert_edge(place, activity, source, rule_id, confidence, counts):
        existing = (
            PlaceActivity.objects.filter(place=place, activity=activity)
            .only("id", "origin", "confidence")
            .first()
        )
        if existing:
            if existing.origin in PROTECTED_ORIGINS:
                return  # never clobber user-confirmed / manual edges
            if confidence < existing.confidence:
                return  # keep the stronger signal (e.g. don't downgrade an OSM edge)
        _, edge_created = PlaceActivity.objects.update_or_create(
            place=place,
            activity=activity,
            defaults={
                "origin": PlaceActivity.Origin.INFERRED,
                "confidence": confidence,
                "source": source,
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
                f" deduped={counts['deduped']}"
            )
            self.stdout.write(
                f"  edges:  created={counts['edge_created']} updated={counts['edge_updated']}"
            )
        if unmapped:
            top = ", ".join(f"{sig}×{n}" for sig, n in unmapped.most_common(8))
            self.stdout.write(f"  unmapped tag-sets ({sum(unmapped.values())} places): {top}")
