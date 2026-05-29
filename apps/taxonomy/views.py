from rest_framework import viewsets
from rest_framework.permissions import AllowAny

from .models import ActivityCategory, ActivityType
from .serializers import ActivityCategorySerializer, ActivityTypeSerializer


class ActivityCategoryViewSet(viewsets.ReadOnlyModelViewSet):
    # Public reference data — explicit AllowAny under deny-by-default DEFAULT_PERMISSION_CLASSES.
    permission_classes = [AllowAny]
    queryset = ActivityCategory.objects.select_related("parent").order_by("slug")
    serializer_class = ActivityCategorySerializer
    lookup_field = "slug"


class ActivityTypeViewSet(viewsets.ReadOnlyModelViewSet):
    permission_classes = [AllowAny]
    queryset = (
        ActivityType.objects.select_related("category", "parent")
        .prefetch_related("relations_out__target")
        .order_by("slug")
    )
    serializer_class = ActivityTypeSerializer
    lookup_field = "slug"
