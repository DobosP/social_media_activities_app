from rest_framework import serializers

from .models import Event


class EventSerializer(serializers.ModelSerializer):
    activity = serializers.SlugRelatedField(
        source="activity_type", slug_field="slug", read_only=True
    )
    place_name = serializers.CharField(source="place.name", read_only=True, default="")

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
            "place",
            "place_name",
            "activity",
        ]
        read_only_fields = fields
