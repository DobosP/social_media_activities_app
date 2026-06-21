from rest_framework import serializers

from .models import AuthorityReferral, ModerationAction, ModerationAppeal, ReasonCode, Report
from .services import APPEAL_MAX_LEN


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
    """Full report view for the staff moderation queue (IsModerator-gated)."""

    target = serializers.SerializerMethodField()
    # F11: advisory triage signals, present only when the list view computed them. Staff-only
    # (this serializer is never served to a reported user); ranks the report, not the person.
    triage = serializers.SerializerMethodField()

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
            "triage",
        ]
        read_only_fields = fields

    def get_target(self, obj):
        return str(obj.target) if obj.target is not None else None

    def get_triage(self, obj):
        return getattr(obj, "_triage", None)


class ResolveReportSerializer(serializers.Serializer):
    DISMISS = "dismiss"
    decision = serializers.ChoiceField(
        choices=[DISMISS, *ModerationAction.Action.values],
    )
    reason = serializers.ChoiceField(choices=ReasonCode.choices, required=False)
    notes = serializers.CharField(required=False, allow_blank=True, default="")
    # Duration for a time-limited restriction (SUSPEND or TIMED_BAN). Required for TIMED_BAN so
    # it can never silently become a never-lifting permanent deactivation outside the BAN ledger.
    suspend_days = serializers.IntegerField(required=False, min_value=1)

    def validate(self, attrs):
        if attrs["decision"] == ModerationAction.Action.TIMED_BAN and not attrs.get("suspend_days"):
            raise serializers.ValidationError(
                {"suspend_days": "A timed ban requires a duration in days."}
            )
        return attrs


class CreateAppealSerializer(serializers.Serializer):
    """A user contests a moderation action that affected them (DSA Art.17)."""

    action_id = serializers.IntegerField(min_value=1)
    statement = serializers.CharField(max_length=APPEAL_MAX_LEN, trim_whitespace=True)


class AppealSerializer(serializers.Serializer):
    """A user's own appeal, allowlisted — no moderator identity or decision_notes (mirrors F19)."""

    action_label = serializers.CharField(read_only=True)
    reason_label = serializers.CharField(read_only=True)
    status = serializers.CharField(read_only=True)
    status_label = serializers.CharField(read_only=True)
    statement = serializers.CharField(read_only=True)
    created_at = serializers.DateTimeField(read_only=True)
    decided_at = serializers.DateTimeField(read_only=True)


class ModerationAppealSerializer(serializers.ModelSerializer):
    """Full appeal view for the staff queue (IsModerator-gated) — includes the appellant + notes."""

    class Meta:
        model = ModerationAppeal
        fields = [
            "id",
            "action",
            "appellant",
            "statement",
            "status",
            "decided_by",
            "decision_notes",
            "decided_at",
            "created_at",
        ]
        read_only_fields = fields


class ResolveAppealSerializer(serializers.Serializer):
    grant = serializers.BooleanField()  # True = overturn (reverse), False = uphold (stands)
    notes = serializers.CharField(required=False, allow_blank=True, default="")


class CreateAuthorityReferralSerializer(serializers.Serializer):
    subject = serializers.UUIDField()  # the subject user's public_id
    reason = serializers.ChoiceField(choices=ReasonCode.choices)
    authority = serializers.ChoiceField(choices=AuthorityReferral.Authority.choices)
    reference = serializers.CharField(required=False, allow_blank=True, default="", max_length=128)
    report_id = serializers.IntegerField(required=False)
    notes = serializers.CharField(required=False, allow_blank=True, default="")
