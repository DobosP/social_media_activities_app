from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.measure import D
from django.utils import timezone
from rest_framework.permissions import AllowAny, IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.events.services import DISCOVERABLE_LIFECYCLE_STATUSES, upcoming_events
from apps.ops.pagination import cursor_response, is_versioned_api_request
from apps.places.models import Place

from .proximity import apply_proximity, parse_point
from .serializers import (
    ActivityCardSerializer,
    ActivityDeckSerializer,
    EventCardSerializer,
    PlaceCardSerializer,
)

# Discovery feeds are read-only projections over existing data. Place/event data is
# public; the activities feed is cohort-scoped + block-aware (reuses social.services).

# The transitional /api/ alias keeps the legacy hard-capped arrays via qs[:MAX_RESULTS]. The
# canonical /api/v1/ surface wraps the same feeds in cursor/limit envelopes capped by
# apps.ops.pagination.MAX_CURSOR_LIMIT.
MAX_RESULTS = 100


def _truthy(params, key) -> bool:
    return params.get(key, "").lower() in ("1", "true", "yes")


class NearMeView(APIView):
    """Places near a point, filterable by activity and venue traits.

    Filters: ?activity=<slug>, ?bookable=true, ?wellness=true, ?family_friendly=true,
    ?has_events=true, ?near_lon=&near_lat=&radius_m=.
    """

    permission_classes = [AllowAny]

    def get(self, request):
        from apps.places.services import public_places

        p = request.query_params
        # F25: never leak a pending user-proposed place's name/coords through discovery.
        qs = public_places(Place.objects.prefetch_related("place_activities__activity"))
        if activity := p.get("activity"):
            # F26: match only via a non-disputed edge (conjoined in one filter()).
            qs = qs.filter(
                place_activities__activity__slug=activity,
                place_activities__is_disputed=False,
            )
        if _truthy(p, "bookable"):
            qs = qs.exclude(website="")
        if _truthy(p, "wellness"):
            qs = qs.filter(place_activities__activity__wellness=True)
        if _truthy(p, "family_friendly"):
            qs = qs.filter(place_activities__activity__family_friendly=True)
        if _truthy(p, "has_events"):
            qs = qs.filter(
                events__starts_at__gte=timezone.now(),
                events__is_tombstone=False,
                events__is_import_held=False,
                events__lifecycle_status__in=DISCOVERABLE_LIFECYCLE_STATUSES,
            )

        qs = qs.distinct()
        qs, point = apply_proximity(qs, p)
        if point is None:
            qs = qs.order_by("id")
        # F32: a SOFT needs-aware nudge — float venues that confirm the viewer's stated access
        # needs to the top, hiding nothing. No-op for an anonymous viewer (get_access_preference
        # returns None). Materialise the capped slice first so the stable partition composes with
        # the distance/id ordering above.
        from apps.places.services import get_access_preference, sort_by_access_match

        if is_versioned_api_request(request):
            # The access-preference sort is a stable partition over a bounded candidate set. It
            # never stores viewer location and never widens the public_places() gate.
            places = sort_by_access_match(
                list(qs[: MAX_RESULTS * 3]), get_access_preference(request.user)
            )
            return cursor_response(request, places, PlaceCardSerializer)
        places = sort_by_access_match(list(qs[:MAX_RESULTS]), get_access_preference(request.user))
        return Response(PlaceCardSerializer(places, many=True).data)


class HappeningView(APIView):
    """Upcoming events ("what's happening"), optionally near a point / by activity.

    Filters: ?activity=<slug>, ?days=<n>, ?near_lon=&near_lat=&radius_m=.
    """

    permission_classes = [AllowAny]

    def get(self, request):
        from datetime import timedelta

        from django.conf import settings
        from django.db.models import Count, Q

        p = request.query_params
        now = timezone.now()
        qs = upcoming_events()
        # F21: drop events the crowd flagged as changed (cancelled/moved/wrong time) from the
        # Happening feed — counted within the decay window so a re-listed event self-heals.
        threshold = getattr(settings, "EVENT_REPORT_THRESHOLD", 3)
        decay = getattr(settings, "EVENT_REPORT_DECAY_SECONDS", 14 * 24 * 3600)
        cutoff = now - timedelta(seconds=decay)
        qs = qs.annotate(
            recent_report_n=Count("reports", filter=Q(reports__created_at__gte=cutoff))
        ).filter(recent_report_n__lt=threshold)
        if activity := p.get("activity"):
            qs = qs.filter(activity_type__slug=activity)
        # W1 search: ?q= free-text filter (title/description/venue; venue already
        # restricted to public places by the F25 gate above).
        if (query := (p.get("q") or "").strip()) and len(query) >= 2:
            qs = qs.filter(
                Q(title__icontains=query)
                | Q(description__icontains=query)
                | Q(place__name__icontains=query)
            )
        if days := p.get("days"):
            try:
                qs = qs.filter(starts_at__lte=now + timezone.timedelta(days=int(days)))
            except (TypeError, ValueError):
                pass

        point = parse_point(p)
        if point is not None:
            qs = qs.filter(place__isnull=False).annotate(
                distance=Distance("place__location", point)
            )
            if radius_m := p.get("radius_m"):
                try:
                    qs = qs.filter(place__location__distance_lte=(point, D(m=float(radius_m))))
                except (TypeError, ValueError):
                    pass
        # Chronological feed regardless of proximity (nearest-soonest stays useful).
        qs = qs.order_by("starts_at")
        if is_versioned_api_request(request):
            return cursor_response(request, qs, EventCardSerializer)
        return Response(EventCardSerializer(qs[:MAX_RESULTS], many=True).data)


class ActivitiesFeedView(APIView):
    """Upcoming activities the user may join — cohort-scoped and block-aware.

    Filters: ?activity=<slug>, ?near_lon=&near_lat=&radius_m=.
    """

    permission_classes = [IsAuthenticated]

    def get(self, request):
        from apps.social.models import Activity
        from apps.social.services import visible_activities

        p = request.query_params
        qs = (
            visible_activities(request.user)
            .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
            .select_related("activity_type", "place", "cover")
        )
        if activity := p.get("activity"):
            qs = qs.filter(activity_type__slug=activity)
        qs, point = apply_proximity(qs, p, field="place__location")
        if point is None:
            qs = qs.order_by("starts_at")
        if is_versioned_api_request(request):
            return cursor_response(
                request, qs, ActivityCardSerializer, context={"request": request}
            )
        return Response(
            ActivityCardSerializer(qs[:MAX_RESULTS], many=True, context={"request": request}).data
        )


class ActivityDeckView(APIView):
    """Mobile card deck over visible activities. Swipes are client-side navigation only."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        from .services import activity_deck

        p = request.query_params
        point = parse_point(p)
        radius_m = None
        if point is not None and p.get("radius_m"):
            try:
                radius_m = float(p.get("radius_m"))
            except (TypeError, ValueError):
                radius_m = None
        deck = activity_deck(
            request.user,
            seed=p.get("seed", ""),
            cursor=p.get("cursor", ""),
            limit=p.get("limit", 12),
            near_point=point,
            radius_m=radius_m,
            activity=p.get("activity") or None,
            beginners=_truthy(p, "beginners"),
        )
        return Response(ActivityDeckSerializer(deck, context={"request": request}).data)


class PublicActivitiesView(APIView):
    """Anonymous (logged-out) discovery of ADULT activities looking for people. A separate,
    viewer-less path from the cohort-walled ActivitiesFeedView — sources from
    social.public_activities(), which hard-codes cohort=ADULT so a minor meetup can never be
    exposed. Optional ?activity=<slug>, ?from=/?to= starts_at window (ISO date or datetime,
    ``to`` exclusive) + request-only proximity. The card serializer carries no owner/PII."""

    permission_classes = [AllowAny]

    def get(self, request):
        from apps.events.views import _parse_bound, _to_exclusive
        from apps.social.services import public_activities

        p = request.query_params
        qs = public_activities()
        if activity := p.get("activity"):
            qs = qs.filter(activity_type__slug=activity)
        if raw_from := p.get("from"):
            qs = qs.filter(starts_at__gte=_parse_bound(raw_from, "from"))
        if raw_to := p.get("to"):
            qs = qs.filter(starts_at__lt=_to_exclusive(raw_to))
        qs, point = apply_proximity(qs, p, field="place__location")
        if point is None:
            qs = qs.order_by("starts_at")
        if is_versioned_api_request(request):
            return cursor_response(
                request, qs, ActivityCardSerializer, context={"request": request}
            )
        return Response(
            ActivityCardSerializer(qs[:MAX_RESULTS], many=True, context={"request": request}).data
        )


class PublicGroupsView(APIView):
    """Anonymous (logged-out) discovery of ADULT standing groups looking for people. Sources from
    social.public_groups() (cohort=ADULT hard-coded). Optional ?activity=<slug> filter; groups
    have no point so no proximity. The card serializer carries no owner/PII."""

    permission_classes = [AllowAny]

    def get(self, request):
        from apps.social.services import public_groups

        p = request.query_params
        qs = public_groups()
        if activity := p.get("activity"):
            qs = qs.filter(activity_type__slug=activity)
        from .serializers import GroupCardSerializer

        if is_versioned_api_request(request):
            return cursor_response(request, qs, GroupCardSerializer)
        return Response(GroupCardSerializer(qs[:MAX_RESULTS], many=True).data)


class HomeFeedView(APIView):
    """W2: the typed home feed for API clients (the future phone app) — the exact same
    ``build_home_feed`` composition the web home page renders, so both surfaces show the
    same items for the same honest reasons. Bounded sections, deterministic order, no
    engagement signals. Optional request-only proximity (?near_lon/near_lat/radius_m)."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        from .serializers import FeedActivitySerializer, FeedEventSerializer
        from .services import build_home_feed

        p = request.query_params
        point = parse_point(p)
        radius_m = None
        if point is not None:
            try:
                radius_m = float(p.get("radius_m") or 10000.0)
            except (TypeError, ValueError):
                radius_m = 10000.0
        feed = build_home_feed(request.user, near_point=point, radius_m=radius_m)
        return Response(
            {
                "recommended": FeedActivitySerializer(
                    feed["recommended"], many=True, context={"request": request}
                ).data,
                "beginners": FeedActivitySerializer(
                    feed["beginners"], many=True, context={"request": request}
                ).data,
                "events": FeedEventSerializer(feed["events"], many=True).data,
                "group_updates": [
                    {
                        "group_id": post.thread.group_id,
                        "group_title": post.thread.group.title,
                        "body": post.body[:280],
                        "created_at": post.created_at,
                    }
                    for post in feed["group_updates"]
                ],
            }
        )
