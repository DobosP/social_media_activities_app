"""Cross-source de-duplication.

The same physical venue often appears in more than one source (OSM *and* Overture,
say). We treat two places as duplicates when they are **spatially close** and have
a **similar name**. Matching is intentionally conservative — better to leave two
records than to merge distinct venues.

``find_duplicate`` is used during ingestion (attach a secondary-source place to an
existing one instead of creating a copy) and by the ``dedup_places`` command for
retroactive cleanup. ``merge_places`` folds a duplicate's activity edges and
provenance into a surviving canonical record.
"""

from __future__ import annotations

import re
import unicodedata
from difflib import SequenceMatcher

from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.geos import Point
from django.contrib.gis.measure import D

DEFAULT_MAX_DISTANCE_M = 75.0
DEFAULT_MIN_NAME_RATIO = 0.82

_WS_RE = re.compile(r"\s+")
_PUNCT_RE = re.compile(r"[^\w\s]", flags=re.UNICODE)


def normalize_name(name: str) -> str:
    """Lowercase, strip accents/punctuation, collapse whitespace — so
    "Café Central" and "cafe central." compare equal."""
    if not name:
        return ""
    decomposed = unicodedata.normalize("NFKD", name)
    ascii_name = "".join(ch for ch in decomposed if not unicodedata.combining(ch))
    ascii_name = _PUNCT_RE.sub(" ", ascii_name.lower())
    return _WS_RE.sub(" ", ascii_name).strip()


def name_similarity(a: str, b: str) -> float:
    """0..1 similarity between two place names (after normalization)."""
    na, nb = normalize_name(a), normalize_name(b)
    if not na or not nb:
        return 0.0
    if na == nb:
        return 1.0
    return SequenceMatcher(None, na, nb).ratio()


def find_duplicate(
    point: Point,
    name: str,
    *,
    exclude_source: str | None = None,
    exclude_pk=None,
    max_distance_m: float = DEFAULT_MAX_DISTANCE_M,
    min_name_ratio: float = DEFAULT_MIN_NAME_RATIO,
):
    """Return the best existing :class:`~apps.places.models.Place` that looks like
    the same venue as ``(point, name)``, or ``None``.

    Considers only places within ``max_distance_m`` and requires a name similarity
    of at least ``min_name_ratio``; among candidates, the closest qualifying name
    match wins.
    """
    from apps.places.models import Place

    if not name or not name.strip():
        return None

    nearby = (
        Place.objects.filter(location__distance_lte=(point, D(m=max_distance_m)))
        .annotate(distance=Distance("location", point))
        .order_by("distance")
    )
    if exclude_source:
        nearby = nearby.exclude(source=exclude_source)
    if exclude_pk is not None:
        nearby = nearby.exclude(pk=exclude_pk)

    for candidate in nearby[:50]:
        if name_similarity(candidate.name, name) >= min_name_ratio:
            return candidate
    return None


def merge_places(canonical, duplicate) -> None:
    """Fold ``duplicate`` into ``canonical``: move activity edges (without clobbering
    protected ones), record the duplicate's source/external id in ``raw_tags`` for
    provenance, then delete the duplicate. Idempotent per edge."""
    from apps.places.models import PlaceActivity

    if canonical.pk == duplicate.pk:
        return

    protected = {PlaceActivity.Origin.CONFIRMED, PlaceActivity.Origin.MANUAL}
    canonical_edges = {edge.activity_id: edge for edge in canonical.place_activities.all()}
    for edge in duplicate.place_activities.all():
        existing = canonical_edges.get(edge.activity_id)
        if existing is None:
            edge.place = canonical
            edge.save(update_fields=["place"])
            canonical_edges[edge.activity_id] = edge
        elif existing.origin not in protected and edge.confidence > existing.confidence:
            existing.confidence = edge.confidence
            existing.origin = edge.origin
            existing.source = edge.source
            existing.mapping_rule = edge.mapping_rule
            existing.save(update_fields=["confidence", "origin", "source", "mapping_rule"])

    merged = canonical.raw_tags.get("merged_sources", []) if canonical.raw_tags else []
    entry = {"source": duplicate.source, "external_id": duplicate.external_id}
    if duplicate.osm_type:
        entry["osm_type"] = duplicate.osm_type
        entry["osm_id"] = duplicate.osm_id
    if entry not in merged:
        merged.append(entry)
    canonical.raw_tags = {**(canonical.raw_tags or {}), "merged_sources": merged}
    if not canonical.opening_hours_raw and duplicate.opening_hours_raw:
        canonical.opening_hours_raw = duplicate.opening_hours_raw
        canonical.opening_hours = duplicate.opening_hours
    canonical.save(update_fields=["raw_tags", "opening_hours_raw", "opening_hours"])

    duplicate.delete()
