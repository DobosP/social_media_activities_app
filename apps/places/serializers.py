from django.utils import timezone
from rest_framework import serializers
from rest_framework_gis.serializers import GeoFeatureModelSerializer

from .enrichment.opening_hours import is_open_at
from .models import Place, PlaceActivity


class PlaceActivitySerializer(serializers.ModelSerializer):
    slug = serializers.SlugRelatedField(source="activity", slug_field="slug", read_only=True)
    name = serializers.CharField(source="activity.name", read_only=True)

    class Meta:
        model = PlaceActivity
        fields = ["slug", "name", "confidence", "origin", "source", "mapping_rule"]


class PlaceSerializer(GeoFeatureModelSerializer):
    activities = PlaceActivitySerializer(source="place_activities", many=True, read_only=True)
    distance_m = serializers.SerializerMethodField()
    open_now = serializers.SerializerMethodField()

    class Meta:
        model = Place
        geo_field = "location"
        fields = [
            "id",
            "name",
            "address_street",
            "address_housenumber",
            "address_city",
            "address_postcode",
            "address_country",
            "opening_hours_raw",
            "opening_hours",
            "open_now",
            "source",
            "osm_type",
            "osm_id",
            "activities",
            "distance_m",
        ]

    def get_distance_m(self, obj):
        distance = getattr(obj, "distance", None)
        if distance is None:
            return None
        return round(distance.m, 1)

    def get_open_now(self, obj):
        # Computed from parsed opening_hours (no external call); null if unknown.
        return is_open_at(obj.opening_hours, timezone.localtime())
