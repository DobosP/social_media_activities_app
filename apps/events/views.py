from datetime import datetime, time, timedelta

from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.geos import Point
from django.contrib.gis.measure import D
from django.db.models import Q
from django.utils import timezone
from django.utils.dateparse import parse_date, parse_datetime
from rest_framework import viewsets
from rest_framework.exceptions import ValidationError
from rest_framework.permissions import AllowAny

from .serializers import EventSerializer
from .services import events_with_public_places, upcoming_events


def _is_bare_date(raw):
    # parse_date rejects anything with a time part; parse_datetime (fromisoformat since
    # Django 5) would accept a bare date too, so date-ness must be checked FIRST. Both
    # parsers RAISE ValueError (not None) on well-shaped but out-of-range input (month 13,
    # Feb 30) — treat that as "not a date" so it reaches the one 400 below.
    try:
        return parse_date(raw) is not None
    except ValueError:
        return False


def _parse_bound(raw, param):
    """``?from=``/``?to=`` accept an ISO datetime or a bare YYYY-MM-DD date. Returns an
    aware datetime (dates anchor to local midnight), or raises a clear 400 — a typo'd
    date must not silently widen an agent's requested window (nor 500 on hour 25)."""
    if _is_bare_date(raw):
        value = datetime.combine(parse_date(raw), time.min)
    else:
        try:
            value = parse_datetime(raw)
        except ValueError:
            value = None
        if value is None:
            raise ValidationError({param: "Use ISO 8601: YYYY-MM-DD or a full datetime."})
    if timezone.is_naive(value):
        value = timezone.make_aware(value)
    return value


def _to_exclusive(raw):
    """The exclusive upper bound for ``?to=``: a bare date means "before that day's local
    midnight + 1 day" (i.e. the whole named day is included); a datetime is used as-is."""
    bound = _parse_bound(raw, "to")
    if _is_bare_date(raw):
        bound += timedelta(days=1)
    return bound


class EventViewSet(viewsets.ReadOnlyModelViewSet):
    """Public happenings at collected places — a deliberately AllowAny read-only surface:
    it exposes exactly the same already-public Event data as the server-rendered web pages
    (/events/), the RSS/Atom feeds, and sitemap.xml, and routes through the sanctioned
    public gate (events_with_public_places(), which hides events at still-unpublished
    user-proposed places) — so anonymous AI agents can query it directly (the global anon
    throttle applies). Events are cohort-blind venue data with no user/child linkage.

    Filters (all optional, composable):
      ?place=<id>            events at one venue
      ?activity=<slug>       events of one activity type
      ?city=<name>           events at venues in that city (case-insensitive)
      ?from= / ?to=          starts_at window (ISO date or datetime; ``to`` is exclusive,
                             a bare ``to`` date means "before that day's local midnight")
      ?q=<text>              free text (>=2 chars) over title/description/venue name —
                             same semantics as services.search_events
      ?near_lon=&near_lat=   nearest-first ordering (request-only coordinates, never stored)
      ?radius_m=             with near_*: keep events within that many metres
      ?include_past=true     lift the default upcoming-only window
    """

    permission_classes = [AllowAny]
    serializer_class = EventSerializer

    def get_queryset(self):
        # F25 gate (review W1-3): same base queryset as every other event surface — an
        # event at a still-unpublished user-proposed place must not leak it here either.
        params = self.request.query_params
        qs = (
            events_with_public_places()
            if params.get("include_past") in ("true", "1")
            else upcoming_events()
        )
        place_id = params.get("place")
        if place_id:
            qs = qs.filter(place_id=place_id)
        activity = params.get("activity")
        if activity:
            qs = qs.filter(activity_type__slug=activity)
        city = (params.get("city") or "").strip()
        if city:
            qs = qs.filter(place__address_city__iexact=city)
        raw_from = params.get("from")
        if raw_from:
            qs = qs.filter(starts_at__gte=_parse_bound(raw_from, "from"))
        raw_to = params.get("to")
        if raw_to:
            qs = qs.filter(starts_at__lt=_to_exclusive(raw_to))
        query = (params.get("q") or "").strip()
        if len(query) >= 2:
            qs = qs.filter(
                Q(title__icontains=query)
                | Q(description__icontains=query)
                | Q(place__name__icontains=query)
            )
        ordering = "starts_at"
        near_lon, near_lat = params.get("near_lon"), params.get("near_lat")
        if near_lon is not None and near_lat is not None:
            # Same request-only proximity contract as PlaceViewSet (never stored) — but on
            # this agent-facing surface malformed values are a clear 400, never a silent
            # empty page or a silently-unbounded radius.
            try:
                point = Point(float(near_lon), float(near_lat), srid=4326)
            except (TypeError, ValueError):
                raise ValidationError(
                    {"near": "Use decimal degrees: ?near_lat=<lat>&near_lon=<lon>."}
                ) from None
            qs = qs.annotate(distance=Distance("place__location", point))
            ordering = "distance"
            radius_m = params.get("radius_m")
            if radius_m:
                try:
                    radius = float(radius_m)
                except (TypeError, ValueError):
                    raise ValidationError({"radius_m": "Use a number of metres."}) from None
                qs = qs.filter(place__location__distance_lte=(point, D(m=radius)))
        return qs.order_by(ordering, "starts_at" if ordering != "starts_at" else "id")
