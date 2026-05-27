from rest_framework import serializers

from .models import ReasonCode, Report


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
