from rest_framework import serializers

from .models import MediaImage


class MediaImageSerializer(serializers.ModelSerializer):
    url = serializers.SerializerMethodField()

    class Meta:
        model = MediaImage
        fields = [
            "public_id",
            "kind",
            "content_type",
            "width",
            "height",
            "byte_size",
            "status",
            "url",
            "created_at",
        ]
        read_only_fields = fields

    def get_url(self, obj) -> str | None:
        viewer = self.context["request"].user
        from .services import MediaError, signed_url

        try:
            return signed_url(viewer, obj)
        except MediaError:
            return None
