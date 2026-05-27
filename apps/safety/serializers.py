from rest_framework import serializers

from .models import ModerationAction, ReasonCode, Report


class ReportSerializer(serializers.ModelSerializer):
    class Meta:
        model = Report
        fields = ["id", "reason", "detail", "status", "created_at"]
        read_only_fields = ["id", "status", "created_at"]


class CreateReportSerializer(serializers.Serializer):
    target_type = serializers.ChoiceField(choices=["user", "activity", "post"])
    target_id = serializers.IntegerField(min_value=1)
    reason = serializers.ChoiceField(choices=ReasonCode.choices)
    detail = serializers.CharField(required=False, allow_blank=True, default="")


class BlockSerializer(serializers.Serializer):
    user_id = serializers.IntegerField(min_value=1)


class ModerationReportSerializer(serializers.ModelSerializer):
    """Full report view for the staff moderation queue."""

    target = serializers.SerializerMethodField()

    class Meta:
        model = Report
        fields = [
            "id",
            "reason",
            "detail",
            "status",
            "target_type",
            "target_id",
            "target",
            "reporter",
            "handled_by",
            "handled_at",
            "resolution",
            "created_at",
        ]
        read_only_fields = fields

    def get_target(self, obj):
        return str(obj.target) if obj.target is not None else None


class ResolveReportSerializer(serializers.Serializer):
    DISMISS = "dismiss"
    decision = serializers.ChoiceField(
        choices=[DISMISS, *ModerationAction.Action.values],
    )
    reason = serializers.ChoiceField(choices=ReasonCode.choices, required=False)
    notes = serializers.CharField(required=False, allow_blank=True, default="")
    suspend_days = serializers.IntegerField(required=False, min_value=1)
