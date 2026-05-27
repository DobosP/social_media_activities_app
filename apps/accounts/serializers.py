from rest_framework import serializers

from .models import User
from .services import can_participate


class MeSerializer(serializers.ModelSerializer):
    requires_parental_consent = serializers.BooleanField(read_only=True)
    can_participate = serializers.SerializerMethodField()
    is_guardian = serializers.BooleanField(read_only=True)

    class Meta:
        model = User
        fields = [
            "public_id",
            "username",
            "display_name",
            "age_band",
            "cohort",
            "role",
            "is_identity_verified",
            "requires_parental_consent",
            "is_guardian",
            "can_participate",
        ]

    def get_can_participate(self, obj) -> bool:
        return can_participate(obj)


class WardSerializer(serializers.ModelSerializer):
    """A minor's profile, as seen/managed by their guardian."""

    can_participate = serializers.SerializerMethodField()

    class Meta:
        model = User
        fields = [
            "public_id",
            "username",
            "display_name",
            "age_band",
            "cohort",
            "is_active",
            "can_participate",
        ]
        read_only_fields = [
            "public_id",
            "username",
            "age_band",
            "cohort",
            "is_active",
            "can_participate",
        ]

    def get_can_participate(self, obj) -> bool:
        return can_participate(obj)
