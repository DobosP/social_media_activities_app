from datetime import timedelta

from django.contrib.auth import get_user_model
from django.utils import timezone
from rest_framework import status
from rest_framework.exceptions import NotFound, Throttled, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.accounts.permissions import IsModerator
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
    take_action,
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


class ReportView(APIView):
    """File a report against a user, activity, or post."""

    permission_classes = [IsAuthenticated]

    def post(self, request):
        serializer = CreateReportSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        if not allow_action(request.user, "report", limit=20, window_seconds=3600):
            raise Throttled(detail="Too many reports; try again later.")
        target = _resolve_target(data["target_type"], data["target_id"])
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
        reports = Report.objects.order_by("-created_at")
        status_filter = request.query_params.get("status")
        if status_filter:
            reports = reports.filter(status=status_filter)
        return Response(ModerationReportSerializer(reports, many=True).data)


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
