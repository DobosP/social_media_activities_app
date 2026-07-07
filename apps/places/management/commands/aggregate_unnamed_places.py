from collections import Counter

from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.geos import Polygon
from django.contrib.gis.measure import D
from django.core.management.base import BaseCommand, CommandError
from django.db import transaction

from apps.places.models import Place, PlaceActivity, PlaceClaim, PlaceCover
from apps.places.services import public_places
from apps.social.models import UserPlaceProposal

SUBVENUE_LEISURE = {"pitch", "track", "court"}
CONTAINER_LEISURE = {"sports_centre", "stadium", "park", "recreation_ground"}
PREFERRED_CONTAINER_LEISURE = {"sports_centre", "stadium"}
SECONDARY_CONTAINER_LEISURE = {"park", "recreation_ground"}
MAX_PARENT_DISTANCE_M = 150


def _tags(place) -> dict:
    return place.raw_tags if isinstance(place.raw_tags, dict) else {}


def _is_subvenue(place) -> bool:
    tags = _tags(place)
    return tags.get("leisure") in SUBVENUE_LEISURE or bool(tags.get("sport"))


def _is_container(place) -> bool:
    tags = _tags(place)
    return tags.get("leisure") in CONTAINER_LEISURE or tags.get("amenity") == "school"


def _container_priority(place) -> int:
    tags = _tags(place)
    if tags.get("leisure") in PREFERRED_CONTAINER_LEISURE:
        return 0
    if tags.get("leisure") in SECONDARY_CONTAINER_LEISURE:
        return 1
    if tags.get("amenity") == "school":
        return 2
    return 3


def _dependency_reasons(place) -> list[str]:
    reasons = []
    if place.social_activities.exists():
        reasons.append("social_activities")
    if PlaceCover.objects.filter(place=place).exists():
        reasons.append("cover")
    if place.corrections.exists():
        reasons.append("corrections")
    if UserPlaceProposal.objects.filter(place=place).exists():
        reasons.append("proposals")
    if PlaceClaim.objects.filter(place=place).exists():
        reasons.append("claims")
    return reasons


class Command(BaseCommand):
    help = "Aggregate unnamed OSM/Overture sport sub-venues into nearby named public complexes."

    def add_arguments(self, parser):
        parser.add_argument("--source", choices=["osm", "overture", "all"], default="all")
        parser.add_argument("--city", default="", help="Optional address_city limiter")
        parser.add_argument(
            "--bbox", default="", help="Optional minlon,minlat,maxlon,maxlat limiter"
        )
        parser.add_argument("--dry-run", action="store_true")

    def handle(self, *args, **opts):
        dry_run = opts["dry_run"]
        children = Place.objects.filter(
            name="", source__in=[Place.Source.OSM, Place.Source.OVERTURE]
        )
        if opts["source"] != "all":
            children = children.filter(source=opts["source"])
        if opts["city"]:
            children = children.filter(address_city__iexact=opts["city"])
        if opts["bbox"]:
            children = children.filter(location__within=self._bbox_polygon(opts["bbox"]))
        children = children.prefetch_related("place_activities__activity__category__parent")

        counts = Counter()
        for child in children.order_by("id"):
            if not _is_subvenue(child):
                counts["skipped_not_subvenue"] += 1
                continue
            parent = self._nearest_parent(child)
            if parent is None:
                counts["skipped_no_parent"] += 1
                continue
            reasons = _dependency_reasons(child)
            if reasons:
                counts["skipped_dependents"] += 1
                self.stdout.write(
                    f"skip place={child.pk}: dependents={','.join(reasons)} parent={parent.pk}"
                )
                continue
            if dry_run:
                counts["would_merge"] += 1
                counts["would_edges"] += child.place_activities.filter(is_disputed=False).count()
                self.stdout.write(f"would merge place={child.pk} into parent={parent.pk}")
                continue
            edge_count = self._merge_and_delete(child.pk, parent.pk)
            counts["merged"] += 1
            counts["edges_copied"] += edge_count

        self._report(dry_run, counts)

    @staticmethod
    def _bbox_polygon(raw_bbox):
        try:
            bbox = tuple(float(part) for part in raw_bbox.split(","))
        except ValueError as exc:
            raise CommandError("--bbox must be 'minlon,minlat,maxlon,maxlat'") from exc
        if len(bbox) != 4:
            raise CommandError("--bbox must have 4 comma-separated numbers")
        polygon = Polygon.from_bbox(bbox)
        polygon.srid = 4326
        return polygon

    def _nearest_parent(self, child):
        candidates = (
            public_places(
                Place.objects.exclude(pk=child.pk)
                .exclude(name="")
                .filter(location__distance_lte=(child.location, D(m=MAX_PARENT_DISTANCE_M)))
            )
            .annotate(distance=Distance("location", child.location))
            .order_by("distance", "id")
        )
        parents = [place for place in candidates if _is_container(place)]
        if not parents:
            return None
        return min(
            parents,
            key=lambda place: (_container_priority(place), place.distance.m, place.id),
        )

    @transaction.atomic
    def _merge_and_delete(self, child_id, parent_id) -> int:
        child = Place.objects.select_for_update().get(pk=child_id)
        parent = Place.objects.select_for_update().get(pk=parent_id)
        reasons = _dependency_reasons(child)
        if reasons:
            return 0
        copied = 0
        edges = child.place_activities.filter(is_disputed=False).select_related("activity")
        for edge in edges:
            existing = PlaceActivity.objects.filter(place=parent, activity=edge.activity).first()
            if existing is None:
                PlaceActivity.objects.create(
                    place=parent,
                    activity=edge.activity,
                    origin=PlaceActivity.Origin.INFERRED,
                    confidence=edge.confidence,
                    source=edge.source,
                    mapping_rule=edge.mapping_rule,
                )
                copied += 1
                continue
            if existing.confidence < edge.confidence:
                existing.confidence = edge.confidence
                existing.source = edge.source
                existing.mapping_rule = edge.mapping_rule
                existing.save(update_fields=["confidence", "source", "mapping_rule"])
                copied += 1
        child.delete()
        return copied

    def _report(self, dry_run, counts):
        self.stdout.write(self.style.MIGRATE_HEADING("Unnamed-place aggregation summary"))
        if dry_run:
            self.stdout.write(
                "  DRY RUN - "
                f"would_merge={counts['would_merge']} would_edges={counts['would_edges']}"
            )
        else:
            self.stdout.write(f"  merged={counts['merged']} edges_copied={counts['edges_copied']}")
        self.stdout.write(
            "  skipped="
            f"no_parent={counts['skipped_no_parent']} "
            f"dependents={counts['skipped_dependents']} "
            f"not_subvenue={counts['skipped_not_subvenue']}"
        )
