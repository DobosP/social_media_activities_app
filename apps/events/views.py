from rest_framework import viewsets
from rest_framework.permissions import IsAuthenticated

from .serializers import EventSerializer
from .services import events_with_public_places, upcoming_events


class EventViewSet(viewsets.ReadOnlyModelViewSet):
    """Public happenings at collected places. Defaults to upcoming events; filter with
    ?place=<id>, ?activity=<slug>, and ?include_past=true."""

    permission_classes = [IsAuthenticated]
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
