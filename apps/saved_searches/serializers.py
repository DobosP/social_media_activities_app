from rest_framework import serializers

from apps.social.models import Activity, ActivityInterest
from apps.taxonomy.models import ActivityCategory, ActivityType

from .models import SavedSearch


class SavedSearchCreateSerializer(serializers.Serializer):
    """Create a saved search. cohort/user are NOT accepted (pinned from the request user in the
    service). Exactly one of activity_type / category — the 2nd of the 3 enforcement layers
    (form + serializer + DB CheckConstraint). Geo is AREA-only via a city string, no coordinate."""

    activity_type = serializers.PrimaryKeyRelatedField(
        queryset=ActivityType.objects.filter(is_active=True), required=False, allow_null=True
    )
    category = serializers.PrimaryKeyRelatedField(
        queryset=ActivityCategory.objects.all(), required=False, allow_null=True
    )
    city = serializers.CharField(required=False, allow_blank=True, max_length=128)
    beginners = serializers.BooleanField(required=False, default=False)
    cost_band = serializers.ChoiceField(
        choices=Activity.CostBand.choices, required=False, allow_blank=True, default=""
    )
    coarse_window = serializers.ChoiceField(
        choices=ActivityInterest.CoarseWindow.choices,
        required=False,
        allow_blank=True,
        default="",
    )

    def validate(self, attrs):
        if bool(attrs.get("activity_type")) == bool(attrs.get("category")):
            raise serializers.ValidationError(
                "Choose exactly one of an activity type or a category."
            )
        return attrs


class SavedSearchSerializer(serializers.ModelSerializer):
    """Read-output. Strict allowlist + read_only — exposes NO match count, 'N near you', or
    last-fired timestamp (F3 forbids counters/digests)."""

    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    category = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    area = serializers.SlugRelatedField(slug_field="slug", read_only=True)

    class Meta:
        model = SavedSearch
        fields = [
            "id",
            "activity_type",
            "category",
            "area",
            "beginners",
            "cost_band",
            "coarse_window",
            "created_at",
        ]
        read_only_fields = fields
