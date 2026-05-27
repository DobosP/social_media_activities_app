from rest_framework import viewsets

from .models import ActivityCategory, ActivityType
from .serializers import ActivityCategorySerializer, ActivityTypeSerializer


class ActivityCategoryViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = ActivityCategory.objects.select_related("parent").order_by("slug")
    serializer_class = ActivityCategorySerializer
    lookup_field = "slug"


class ActivityTypeViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = (
        ActivityType.objects.select_related("category", "parent")
        .prefetch_related("relations_out__target")
        .order_by("slug")
    )
    serializer_class = ActivityTypeSerializer
    lookup_field = "slug"
