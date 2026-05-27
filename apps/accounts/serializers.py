from rest_framework import serializers

from .models import User
from .services import can_participate


class MeSerializer(serializers.ModelSerializer):
    requires_parental_consent = serializers.BooleanField(read_only=True)
    can_participate = serializers.SerializerMethodField()

    class Meta:
        model = User
        fields = [
            "public_id",
            "username",
            "display_name",
            "age_band",
            "cohort",
            "is_identity_verified",
            "requires_parental_consent",
            "can_participate",
        ]

    def get_can_participate(self, obj) -> bool:
        return can_participate(obj)
