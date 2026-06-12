from rest_framework import serializers


def _distance_m(obj):
    distance = getattr(obj, "distance", None)
    return round(distance.m, 1) if distance is not None else None


class PlaceCardSerializer(serializers.Serializer):
    """A place as it appears in a discovery feed (lightweight, not full GeoJSON)."""

    id = serializers.IntegerField()
    name = serializers.CharField()
    address_city = serializers.CharField()
    lon = serializers.SerializerMethodField()
    lat = serializers.SerializerMethodField()
    distance_m = serializers.SerializerMethodField()
    is_bookable = serializers.BooleanField()
    website = serializers.CharField()
    activities = serializers.SerializerMethodField()

    def get_lon(self, obj):
        return obj.location.x if obj.location else None

    def get_lat(self, obj):
        return obj.location.y if obj.location else None

    def get_distance_m(self, obj):
        return _distance_m(obj)

    def get_activities(self, obj):
        # F26: disputed edges are hidden from discovery.
        return [pa.activity.slug for pa in obj.place_activities.all() if not pa.is_disputed]


class EventCardSerializer(serializers.Serializer):
    id = serializers.IntegerField()
    title = serializers.CharField()
    starts_at = serializers.DateTimeField()
    ends_at = serializers.DateTimeField()
    url = serializers.CharField()
    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    place_id = serializers.IntegerField()
    place_name = serializers.CharField(source="place.name", default=None)
    distance_m = serializers.SerializerMethodField()

    def get_distance_m(self, obj):
        return _distance_m(obj)


class ActivityCardSerializer(serializers.Serializer):
    id = serializers.IntegerField()
    title = serializers.CharField()
    cohort = serializers.CharField()
    starts_at = serializers.DateTimeField()
    status = serializers.CharField()
    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    place_id = serializers.IntegerField()
    distance_m = serializers.SerializerMethodField()

    def get_distance_m(self, obj):
        return _distance_m(obj)


class FeedActivitySerializer(ActivityCardSerializer):
    """W2 home feed card: the activity card + the F17 honest reason. Strict allowlist —
    no member counts, no popularity signals (inv.2)."""

    place_name = serializers.CharField(source="place.name", default=None)
    reason = serializers.SerializerMethodField()

    def get_reason(self, obj):
        return getattr(obj, "rec_reason", "")


class FeedEventSerializer(EventCardSerializer):
    """W2 home feed event card + its honest interest-match reason (may be empty)."""

    reason = serializers.SerializerMethodField()

    def get_reason(self, obj):
        return getattr(obj, "feed_reason", "")
