from rest_framework import serializers
from rest_framework_gis.serializers import GeoFeatureModelSerializer

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
