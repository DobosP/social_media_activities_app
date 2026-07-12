from rest_framework import viewsets
from rest_framework.permissions import AllowAny

from .serializers import EventSerializer
from .services import events_with_public_places, upcoming_events


class EventViewSet(viewsets.ReadOnlyModelViewSet):
    """Public happenings at collected places. Defaults to upcoming events; filter with
    ?place=<id>, ?activity=<slug>, and ?include_past=true.

    Deliberately AllowAny: this read-only surface exposes exactly the same
    already-public Event data as the server-rendered web pages (/events/), the
    RSS/Atom feeds, and sitemap.xml, and it routes through the sanctioned public gates
    (events_with_public_places()/upcoming_events()) that exclude unpublished
    user-proposed places, tombstones, held imports, and non-discoverable lifecycle
    statuses — so anonymous AI agents can consume it (the global anon throttle applies).
    Events are cohort-blind venue data with no user/child linkage."""

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
        return qs.order_by("starts_at")
