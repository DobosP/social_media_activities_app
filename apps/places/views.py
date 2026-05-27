from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.geos import Point
from django.contrib.gis.measure import D
from django_filters.rest_framework import DjangoFilterBackend
from rest_framework import viewsets
from rest_framework_gis.filters import InBBoxFilter
from rest_framework_gis.pagination import GeoJsonPagination

from .filters import PlaceFilter
from .models import Place
from .serializers import PlaceSerializer


class PlaceViewSet(viewsets.ReadOnlyModelViewSet):
    """Read-only places API returning GeoJSON.

    Filtering: ?activity=<slug>, ?city=, ?source=, ?min_confidence=, ?in_bbox=.
    Proximity: ?near_lon=&near_lat= orders nearest-first and adds distance_m;
    add ?radius_m= to also filter within that radius (metres).
    """

    serializer_class = PlaceSerializer
    pagination_class = GeoJsonPagination
    filter_backends = [DjangoFilterBackend, InBBoxFilter]
    filterset_class = PlaceFilter
    bbox_filter_field = "location"
    bbox_filter_include_overlapping = True

    def get_queryset(self):
        qs = Place.objects.prefetch_related("place_activities__activity").order_by("id")
        params = self.request.query_params
        near_lon, near_lat = params.get("near_lon"), params.get("near_lat")
        if near_lon is not None and near_lat is not None:
            try:
                point = Point(float(near_lon), float(near_lat), srid=4326)
            except (TypeError, ValueError):
                return qs.none()
            qs = qs.annotate(distance=Distance("location", point)).order_by("distance")
            radius_m = params.get("radius_m")
            if radius_m:
                try:
                    qs = qs.filter(location__distance_lte=(point, D(m=float(radius_m))))
                except (TypeError, ValueError):
                    pass
        return qs.distinct()
