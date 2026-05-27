"""Shared proximity parsing for discovery endpoints — reuses the PostGIS geography
distance query already used by the places API (index-backed via the GiST index)."""

from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.geos import Point
from django.contrib.gis.measure import D


def parse_point(params) -> Point | None:
    """Build a SRID-4326 point from ?near_lon/&near_lat, or None if absent/invalid."""
    lon, lat = params.get("near_lon"), params.get("near_lat")
    if lon is None or lat is None:
        return None
    try:
        return Point(float(lon), float(lat), srid=4326)
    except (TypeError, ValueError):
        return None


def apply_proximity(qs, params, *, field="location"):
    """Order a queryset by distance to the point and optionally filter by ?radius_m.

    Returns (queryset, point). When no point is supplied the queryset is unchanged.
    """
    point = parse_point(params)
    if point is None:
        return qs, None
    qs = qs.annotate(distance=Distance(field, point)).order_by("distance")
    radius_m = params.get("radius_m")
    if radius_m:
        try:
            qs = qs.filter(**{f"{field}__distance_lte": (point, D(m=float(radius_m)))})
        except (TypeError, ValueError):
            pass
    return qs, point
