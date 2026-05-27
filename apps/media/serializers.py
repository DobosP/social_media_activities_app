from rest_framework import serializers

from .models import Photo


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
