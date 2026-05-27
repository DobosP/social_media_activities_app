from django.utils import timezone
from rest_framework import viewsets
from rest_framework.permissions import IsAuthenticated

from .models import Event
from .serializers import EventSerializer


class EventViewSet(viewsets.ReadOnlyModelViewSet):
    """Public happenings at collected places. Defaults to upcoming events; filter with
    ?place=<id>, ?activity=<slug>, and ?include_past=true."""

    permission_classes = [IsAuthenticated]
    serializer_class = EventSerializer

    def get_queryset(self):
        qs = Event.objects.select_related("place", "activity_type")
        params = self.request.query_params
        if params.get("include_past") not in ("true", "1"):
            qs = qs.filter(starts_at__gte=timezone.now())
        place_id = params.get("place")
        if place_id:
            qs = qs.filter(place_id=place_id)
        activity = params.get("activity")
        if activity:
            qs = qs.filter(activity_type__slug=activity)
        return qs.order_by("starts_at")
