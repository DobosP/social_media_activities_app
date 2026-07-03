from rest_framework import serializers

from .models import ActivityCover, Photo


class PhotoSerializer(serializers.ModelSerializer):
    url = serializers.SerializerMethodField()

    class Meta:
        model = Photo
        fields = [
            "id",
            "kind",
            "thread",
            "content_type",
            "byte_size",
            "width",
            "height",
            "scan_status",
            "created_at",
            "url",
        ]
        read_only_fields = fields

    def get_url(self, obj):
        # Populated by the view (which holds the request/viewer) when authorized.
        return self.context.get("signed_urls", {}).get(obj.id)


class ActivityCoverSerializer(serializers.ModelSerializer):
    url = serializers.SerializerMethodField()
    activity = serializers.IntegerField(source="activity_id", read_only=True)

    class Meta:
        model = ActivityCover
        fields = [
            "id",
            "activity",
            "content_type",
            "byte_size",
            "width",
            "height",
            "alt_text",
            "created_at",
            "updated_at",
            "url",
        ]
        read_only_fields = fields

    def get_url(self, obj):
        return self.context.get("signed_urls", {}).get(obj.id)
