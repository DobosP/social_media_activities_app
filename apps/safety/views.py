from datetime import timedelta

from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework import status
from rest_framework.exceptions import NotFound, Throttled, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.accounts.permissions import IsModerator
from apps.social import services as social
from apps.social.models import Activity, Post

from .models import AuthorityReferral, ModerationAction, ModerationAppeal, Report
from .serializers import (
    AppealSerializer,
    BlockSerializer,
    CreateAppealSerializer,
    CreateAuthorityReferralSerializer,
    CreateReportSerializer,
    ModerationAppealSerializer,
    ModerationReportSerializer,
    ReportSerializer,
    ResolveAppealSerializer,
    ResolveReportSerializer,
)
from .services import (
    AppealError,
    _affected_user,
    allow_action,
    appeals_for,
    block_user,
    create_authority_referral,
    dismiss_report,
    file_appeal,
    file_report,
    record_audit,
    referral_proof_pack,
    resolve_appeal,
    take_action,
    triage_order,
    unblock_user,
)

User = get_user_model()

_TARGET_MODELS = {"user": User, "activity": Activity, "post": Post}


def _resolve_target(target_type, target_id):
    model = _TARGET_MODELS[target_type]
    try:
        return model.objects.get(pk=target_id)
    except model.DoesNotExist as exc:
        raise ValidationError({"target_id": "No such target."}) from exc


def _resolve_report_target(user, target_type, target_id):
    """Resolve a report target gated by the reporter's visibility, mirroring the web UI's
    ``_resolve_report_target``. Returns the target or None when it is unknown or the user
    is not allowed to see it (so the API can 404 rather than leak existence)."""
    if target_type == "activity":
        activity = Activity.objects.filter(pk=target_id).first()
        if activity and (user.is_staff or social.can_see_activity(user, activity)):
            return activity
    elif target_type == "post":
        post = Post.objects.filter(pk=target_id).first()
        if post:
            # A Post can live in an activity thread OR a group thread — resolve the owner
            # generically (a group thread's .activity is None). can_see_activity is a cohort check,
            # which both an Activity and a Group satisfy, so a same-cohort member can report either.
            owner = post.thread.owner_object
            if user.is_staff or social.can_see_activity(user, owner):
                return post
    elif target_type == "user":
        person = User.objects.filter(pk=target_id).first()
        if person:
            return person
    return None


class ReportView(APIView):
    """File a report against a user, activity, or post."""

    permission_classes = [IsAuthenticated]

    def post(self, request):
        serializer = CreateReportSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        if not allow_action(request.user, "report", limit=20, window_seconds=3600):
            raise Throttled(detail="Too many reports; try again later.")
        target = _resolve_report_target(request.user, data["target_type"], data["target_id"])
        if target is None:
            # Don't leak whether the target exists but is invisible to the reporter.
            raise NotFound("Nothing to report.")
        report = file_report(request.user, target, data["reason"], data["detail"])
        return Response(ReportSerializer(report).data, status=status.HTTP_201_CREATED)


class BlockView(APIView):
    """Block or unblock another user."""

    permission_classes = [IsAuthenticated]

    def post(self, request):
        serializer = BlockSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        target = _resolve_target("user", serializer.validated_data["user_id"])
        try:
            block_user(request.user, target)
        except ValueError as exc:
            raise ValidationError(str(exc)) from exc
        return Response(status=status.HTTP_204_NO_CONTENT)

    def delete(self, request):
        serializer = BlockSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        target = _resolve_target("user", serializer.validated_data["user_id"])
        unblock_user(request.user, target)
        return Response(status=status.HTTP_204_NO_CONTENT)


class ModerationReportListView(APIView):
    """Staff review queue (read API). `?status=open` filters; default lists all."""

    permission_classes = [IsModerator]

    def get(self, request):
        from django.db import transaction
        from django.db.models import Case, IntegerField, Value, When

        from .services import _TRIAGE_SEVERITY

        reports = Report.objects.all()
        status_filter = request.query_params.get("status")
        if status_filter:
            reports = reports.filter(status=status_filter)
        # F11: pre-order by reason severity in the DB BEFORE the hard cap, so the most dangerous
        # reasons (CSAM/grooming) can never be truncated by a recency-only cap; the full advisory
        # triage re-rank (child involvement + duplicate count + contact hint) happens in Python.
        severity = Case(
            *[When(reason=k, then=Value(v)) for k, v in _TRIAGE_SEVERITY.items()],
            default=Value(0),
            output_field=IntegerField(),
        )
        capped = list(reports.annotate(_sev=severity).order_by("-_sev", "-created_at")[:100])
        ordered = triage_order(capped)  # [(report, summary), ...] most-dangerous-first
        for report, summary in ordered:
            report._triage = summary
        # DSA accountability: audit staff access to the queue (no report content in the log).
        with transaction.atomic():
            record_audit(
                "moderation.queue_viewed",
                actor=request.user,
                status=status_filter or "all",
                count=len(ordered),
            )
        return Response(ModerationReportSerializer([r for r, _ in ordered], many=True).data)


class ResolveReportView(APIView):
    """Staff: resolve a report by dismissing it or taking a moderation action."""

    permission_classes = [IsModerator]

    def post(self, request, pk):
        report = Report.objects.filter(pk=pk).first()
        if report is None:
            raise NotFound("No such report.")
        serializer = ResolveReportSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data

        if data["decision"] == ResolveReportSerializer.DISMISS:
            dismiss_report(request.user, report, data.get("notes", ""))
            return Response(ModerationReportSerializer(report).data)

        if report.target is None:
            raise ValidationError("The report's target no longer exists.")
        expires_at = None
        timed = (ModerationAction.Action.SUSPEND, ModerationAction.Action.TIMED_BAN)
        if data["decision"] in timed and data.get("suspend_days"):
            expires_at = timezone.now() + timedelta(days=data["suspend_days"])
        take_action(
            request.user,
            report.target,
            data["decision"],
            data.get("reason", report.reason),
            notes=data.get("notes", ""),
            report=report,
            expires_at=expires_at,
        )
        report.refresh_from_db()
        return Response(ModerationReportSerializer(report).data)


class AppealView(APIView):
    """A user's own DSA Art.17 redress: file a contest against a decision that affected them, and
    read back their own appeals. Strictly self-scoped (a logged-in surface — the suspended pre-auth
    path is web-only, since a deactivated account has no API token)."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        return Response(AppealSerializer(appeals_for(request.user), many=True).data)

    def post(self, request):
        serializer = CreateAppealSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        action = ModerationAction.objects.filter(pk=data["action_id"]).first()
        # 404 (not 403) when the action doesn't exist OR didn't affect the caller, so the endpoint
        # never reveals the existence of someone else's moderation action.
        if action is None or _affected_user(action.target) != request.user:
            raise NotFound("No such decision.")
        try:
            appeal = file_appeal(request.user, action, data["statement"])
        except AppealError as exc:
            raise ValidationError(str(exc)) from exc
        return Response(
            {"status": appeal.status, "detail": "Your appeal was received."},
            status=status.HTTP_201_CREATED,
        )


class ModerationAppealListView(APIView):
    """Staff: the DSA Art.17 internal complaint-handling queue. `?status=pending` (default lists
    all). Oldest-first within the default so contests are worked fairly in order."""

    permission_classes = [IsModerator]

    def get(self, request):
        appeals = ModerationAppeal.objects.select_related("action").order_by("created_at")
        status_filter = request.query_params.get("status")
        if status_filter:
            appeals = appeals.filter(status=status_filter)
        return Response(ModerationAppealSerializer(appeals[:200], many=True).data)


class ResolveAppealView(APIView):
    """Staff: decide an appeal — uphold (decision stands) or grant (overturn → reverse)."""

    permission_classes = [IsModerator]

    def post(self, request, pk):
        appeal = ModerationAppeal.objects.filter(pk=pk).first()
        if appeal is None:
            raise NotFound("No such appeal.")
        serializer = ResolveAppealSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        try:
            resolve_appeal(request.user, appeal, grant=data["grant"], notes=data.get("notes", ""))
        except AppealError as exc:
            raise ValidationError(str(exc)) from exc
        appeal.refresh_from_db()
        return Response(ModerationAppealSerializer(appeal).data)


class AuthorityReferralView(APIView):
    """Staff: refer a user to an external authority (real-world legal consequence) and read
    back the tamper-evident proof pack for a lawful request. The subject is never notified."""

    permission_classes = [IsModerator]

    def post(self, request):
        serializer = CreateAuthorityReferralSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        subject = User.objects.filter(public_id=data["subject"]).first()
        if subject is None:
            raise NotFound("No such user.")
        report = None
        if data.get("report_id"):
            report = Report.objects.filter(pk=data["report_id"]).first()
        referral = create_authority_referral(
            request.user,
            subject,
            data["reason"],
            authority=data["authority"],
            report=report,
            reference=data.get("reference", ""),
            notes=data.get("notes", ""),
        )
        return Response(referral_proof_pack(referral), status=status.HTTP_201_CREATED)

    def get(self, request, pk):
        referral = AuthorityReferral.objects.filter(pk=pk).first()
        if referral is None:
            raise NotFound("No such referral.")
        record_audit("authority.referral_proof_viewed", actor=request.user, target=referral)
        return Response(referral_proof_pack(referral))
