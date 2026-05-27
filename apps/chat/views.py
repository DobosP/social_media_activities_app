from django.shortcuts import get_object_or_404
from rest_framework import status
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.social.models import Thread

from . import services
from .serializers import ChatMessageSerializer


class ThreadMessagesView(APIView):
    """HTTP fallback for chat: read history and post a message. Real-time delivery
    is over the WebSocket consumer; this shares the same access rules and service."""

    permission_classes = [IsAuthenticated]

    def get(self, request, thread_id):
        thread = get_object_or_404(Thread.objects.select_related("activity"), pk=thread_id)
        if not services.can_access_thread(request.user, thread):
            return Response({"detail": "Forbidden."}, status=status.HTTP_403_FORBIDDEN)
        messages = services.message_history(thread)
        return Response(ChatMessageSerializer(messages, many=True).data)

    def post(self, request, thread_id):
        thread = get_object_or_404(Thread.objects.select_related("activity"), pk=thread_id)
        try:
            message = services.send_message(request.user, thread, request.data.get("body", ""))
        except services.ChatError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(ChatMessageSerializer(message).data, status=status.HTTP_201_CREATED)
