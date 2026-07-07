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
    activities = serializers.SerializerMethodField()
    distance_m = serializers.SerializerMethodField()
    open_now = serializers.SerializerMethodField()
    categories = serializers.SerializerMethodField()
    category_labels = serializers.SerializerMethodField()
    has_upcoming = serializers.SerializerMethodField()
    # F20 / W3-F14: render the crowd-corrected name/address/hours (each falls back to raw OSM) so
    # the corrected schedule and `open_now` agree. `opening_hours_raw` stays the canonical OSM text.
    name = serializers.CharField(source="display_name", read_only=True)
    display_address = serializers.CharField(read_only=True)
    opening_hours = serializers.JSONField(source="display_opening_hours", read_only=True)
    attribution_credit = serializers.SerializerMethodField()

    def get_activities(self, obj):
        # F26: disputed edges are hidden from discovery. Filter in Python over the prefetch.
        edges = [pa for pa in obj.place_activities.all() if not pa.is_disputed]
        return PlaceActivitySerializer(edges, many=True).data

    def _category_pairs(self, obj):
        pairs = {}
        for edge in obj.place_activities.all():
            if edge.is_disputed:
                continue
            category = edge.activity.category
            top = category.parent or category
            pairs.setdefault(top.slug, top.name)
        return pairs

    def get_categories(self, obj):
        return list(self._category_pairs(obj).keys())

    def get_category_labels(self, obj):
        return list(self._category_pairs(obj).values())

    def get_has_upcoming(self, obj):
        return bool(getattr(obj, "has_upcoming", False))

    class Meta:
        model = Place
        geo_field = "location"
        fields = [
            "id",
            "name",
            "display_address",
            "address_street",
            "address_housenumber",
            "address_city",
            "address_postcode",
            "address_country",
            "opening_hours_raw",
            "opening_hours",
            "open_now",
            "categories",
            "category_labels",
            "has_upcoming",
            "website",
            "phone",
            "is_bookable",
            "source",
            "osm_type",
            "osm_id",
            "attribution",
            "license_name",
            "provenance_url",
            "attribution_credit",
            "activities",
            "distance_m",
        ]

    def get_distance_m(self, obj):
        distance = getattr(obj, "distance", None)
        if distance is None:
            return None
        return round(distance.m, 1)

    def get_open_now(self, obj):
        # F28: parsed-hours open/closed, downgraded to 'unverified' when enough recent member
        # reports say the hours are wrong (null if unknown). No external call.
        from .services import open_now_status

        return open_now_status(obj)

    def get_attribution_credit(self, obj):
        from .services import place_attribution

        return place_attribution(obj)
