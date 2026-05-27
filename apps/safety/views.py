from django.contrib.auth import get_user_model
from rest_framework import status
from rest_framework.exceptions import Throttled, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.social.models import Activity, Post

from .serializers import BlockSerializer, CreateReportSerializer, ReportSerializer
from .services import allow_action, block_user, file_report, unblock_user

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
