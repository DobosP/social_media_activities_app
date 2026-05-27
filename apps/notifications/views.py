from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import Notification
from .serializers import (
    MarkReadSerializer,
    NotificationSerializer,
    PreferenceSerializer,
)
from .services import get_preferences, mark_read, unread_count


class NotificationViewSet(viewsets.ReadOnlyModelViewSet):
    """The current user's in-app inbox."""

    permission_classes = [IsAuthenticated]
    serializer_class = NotificationSerializer

    def get_queryset(self):
        return Notification.objects.filter(recipient=self.request.user)

    @action(detail=False, methods=["get"])
    def unread_count(self, request):
        return Response({"unread": unread_count(request.user)})

    @action(detail=False, methods=["post"])
    def mark_read(self, request):
        form = MarkReadSerializer(data=request.data)
        form.is_valid(raise_exception=True)
        count = mark_read(request.user, form.validated_data.get("ids"))
        return Response({"marked": count})


class PreferenceView(APIView):
    """Get/update the current user's opt-in notification preferences."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        return Response(PreferenceSerializer(get_preferences(request.user)).data)

    def put(self, request):
        pref = get_preferences(request.user)
        form = PreferenceSerializer(pref, data=request.data, partial=True)
        form.is_valid(raise_exception=True)
        form.save()
        return Response(form.data, status=status.HTTP_200_OK)
