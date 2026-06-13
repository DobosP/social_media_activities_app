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

from .models import ModerationAction, Report
from .serializers import (
    BlockSerializer,
    CreateReportSerializer,
    ModerationReportSerializer,
    ReportSerializer,
    ResolveReportSerializer,
)
from .services import (
    allow_action,
    block_user,
    dismiss_report,
    file_report,
    record_audit,
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
        if data["decision"] == ModerationAction.Action.SUSPEND and data.get("suspend_days"):
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
