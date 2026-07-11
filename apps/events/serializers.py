from rest_framework import serializers

from .models import Event


class EventSerializer(serializers.ModelSerializer):
    activity = serializers.SlugRelatedField(
        source="activity_type", slug_field="slug", read_only=True
    )
    place_name = serializers.CharField(source="place.name", read_only=True, default="")
    attribution_credit = serializers.SerializerMethodField()

    class Meta:
        model = Event
        fields = [
            "id",
            "title",
            "description",
            "starts_at",
            "ends_at",
            "url",
            "source",
            "source_category",
            "lifecycle_status",
            "source_confidence",
            "attribution",
            "license_name",
            "provenance_url",
            "attribution_credit",
            "place",
            "place_name",
            "activity",
        ]
        read_only_fields = fields

    def get_attribution_credit(self, obj):
        from .services import event_attribution

        return event_attribution(obj)
