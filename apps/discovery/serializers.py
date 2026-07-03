from rest_framework import serializers

from apps.media.services import activity_visual


def _viewer_from_context(context):
    request = context.get("request") if context else None
    if request is None or not request.user.is_authenticated:
        return None
    return request.user


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
    visual = serializers.SerializerMethodField()

    def get_distance_m(self, obj):
        return _distance_m(obj)

    def get_visual(self, obj):
        return activity_visual(obj, _viewer_from_context(self.context))


class FeedActivitySerializer(ActivityCardSerializer):
    """W2 home feed card: the activity card + the F17 honest reason. Strict allowlist."""

    place_name = serializers.CharField(source="place.name", default=None)
    reason = serializers.SerializerMethodField()

    def get_reason(self, obj):
        return getattr(obj, "rec_reason", "")


class FeedEventSerializer(EventCardSerializer):
    """W2 home feed event card + its honest interest-match reason (may be empty)."""

    reason = serializers.SerializerMethodField()

    def get_reason(self, obj):
        return getattr(obj, "feed_reason", "")


class GroupCardSerializer(serializers.Serializer):
    """A standing group as it appears in the anonymous public discovery feed."""

    id = serializers.IntegerField()
    title = serializers.CharField()
    cohort = serializers.CharField()
    city = serializers.CharField(source="area.name", default=None)
    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    description = serializers.SerializerMethodField()

    def get_description(self, obj):
        return (obj.description or "")[:280]


class ActivityDeckItemSerializer(serializers.Serializer):
    id = serializers.IntegerField()
    title = serializers.CharField()
    description = serializers.SerializerMethodField()
    starts_at = serializers.DateTimeField()
    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    place_id = serializers.IntegerField()
    place_name = serializers.CharField(source="place.name", default=None)
    distance_m = serializers.SerializerMethodField()
    visual = serializers.SerializerMethodField()
    actions = serializers.SerializerMethodField()

    def get_description(self, obj):
        return (obj.description or "")[:280]

    def get_distance_m(self, obj):
        return _distance_m(obj)

    def get_visual(self, obj):
        return activity_visual(obj, _viewer_from_context(self.context))

    def get_actions(self, obj):
        return {
            "detail_url": f"/api/v1/social/activities/{obj.id}/",
            "web_url": f"/activities/{obj.id}/",
        }


class ActivityDeckSerializer(serializers.Serializer):
    deck_seed = serializers.CharField()
    next_cursor = serializers.CharField(allow_blank=True)
    items = ActivityDeckItemSerializer(many=True)
