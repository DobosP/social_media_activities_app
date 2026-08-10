from django.contrib.contenttypes.models import ContentType
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


def _author_deleted_hidden_action_ids(actions) -> set[int]:
    """Pks of the REMOVE actions among ``actions`` whose content target is BOTH author-deleted
    and still hidden — one query per content type, never one per action row. The is_hidden gate
    mirrors ``_reverse_action`` (services): it only declines the un-hide while the content IS
    hidden, so an operator-un-hidden (live) post must not be presented as one an overturn
    "will not republish"."""
    by_ct: dict[int, list] = {}
    for action in actions:
        if action.action == ModerationAction.Action.REMOVE:
            by_ct.setdefault(action.target_type_id, []).append(action)
    flagged: set[int] = set()
    for ct_id, removes in by_ct.items():
        model = ContentType.objects.get_for_id(ct_id).model_class()
        if model is None or not hasattr(model, "is_author_deleted"):
            continue
        hidden_deleted = set(
            model._default_manager.filter(
                pk__in={a.target_id for a in removes},
                is_author_deleted=True,
                is_hidden=True,
            ).values_list("pk", flat=True)
        )
        flagged.update(a.pk for a in removes if a.target_id in hidden_deleted)
    return flagged


class ModerationAppealListSerializer(serializers.ListSerializer):
    """Batches content_author_deleted for the staff queue: the list view serialises up to 200
    appeals (apps/safety/views.py), and resolving each appeal's generic-FK target per row would
    be 200 extra queries. Installed via Meta.list_serializer_class so every many=True caller
    gets the batch; the single-instance path keeps the per-row fallback."""

    def to_representation(self, data):
        appeals = list(data)
        # The view select_related's "action", so this resolves no extra queries per row.
        self.child._author_deleted_action_ids = _author_deleted_hidden_action_ids(
            [appeal.action for appeal in appeals]
        )
        return super().to_representation(appeals)


class ModerationAppealSerializer(serializers.ModelSerializer):
    """Full appeal view for the staff queue (IsModerator-gated) — includes the appellant + notes."""

    # Pre-decision context: True when overturning this appeal will NOT republish the content,
    # because its author also deleted it themselves AND it is still hidden (the reversal
    # declines the un-hide; a live, operator-un-hidden post has nothing left to republish).
    # Only this boolean crosses — nothing else of the target is serialized here.
    content_author_deleted = serializers.SerializerMethodField()

    class Meta:
        model = ModerationAppeal
        list_serializer_class = ModerationAppealListSerializer
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
            "content_author_deleted",
        ]
        read_only_fields = fields

    def get_content_author_deleted(self, obj):
        batched = getattr(self, "_author_deleted_action_ids", None)
        if batched is not None:
            return obj.action_id in batched
        action = obj.action
        if action.action != ModerationAction.Action.REMOVE:
            return False
        target = action.target
        return bool(getattr(target, "is_author_deleted", False)) and bool(
            getattr(target, "is_hidden", False)
        )


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
