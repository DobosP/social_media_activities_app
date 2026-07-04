from rest_framework.exceptions import NotFound
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.ops.pagination import cursor_page, is_versioned_api_request

from .models import Notification
from .serializers import NotificationSerializer
from .services import mark_all_read, mark_read, unread_count


class NotificationListView(APIView):
    """The current user's notifications. `?unread=true` filters to unread."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        qs = Notification.objects.filter(recipient=request.user).order_by("-created_at", "-id")
        if request.query_params.get("unread") in ("true", "1"):
            qs = qs.filter(read_at__isnull=True)
        if is_versioned_api_request(request):
            page, next_cursor, limit = cursor_page(request, qs)
            return Response(
                {
                    "unread_count": unread_count(request.user),
                    "next_cursor": next_cursor,
                    "limit": limit,
                    "results": NotificationSerializer(page, many=True).data,
                }
            )
        return Response(
            {
                "unread_count": unread_count(request.user),
                "results": NotificationSerializer(qs[:100], many=True).data,
            }
        )


class MarkReadView(APIView):
    permission_classes = [IsAuthenticated]

    def post(self, request, pk):
        notification = Notification.objects.filter(pk=pk, recipient=request.user).first()
        if notification is None:
            raise NotFound("No such notification.")
        mark_read(notification)
        return Response(NotificationSerializer(notification).data)


class MarkAllReadView(APIView):
    permission_classes = [IsAuthenticated]

    def post(self, request):
        count = mark_all_read(request.user)
        return Response({"marked_read": count})
