from channels.db import database_sync_to_async
from channels.generic.websocket import AsyncJsonWebsocketConsumer

from apps.social.models import Thread

from .serializers import ChatMessageSerializer
from .services import ChatError, can_access_thread, send_message


class ChatConsumer(AsyncJsonWebsocketConsumer):
    """Per-thread WebSocket room, private to the activity's members.

    Membership + cohort are checked on connect; each inbound message is persisted
    and moderated via the chat service, then fanned out to the room group.
    """

    async def connect(self):
        self.user = self.scope.get("user")
        self.thread_id = self.scope["url_route"]["kwargs"]["thread_id"]
        self.group_name = f"chat_{self.thread_id}"

        self.thread = await self._get_thread(self.thread_id)
        if self.thread is None or not await self._can_access(self.user, self.thread):
            await self.close(code=4403)
            return

        await self.channel_layer.group_add(self.group_name, self.channel_name)
        await self.accept()

    async def disconnect(self, code):
        if hasattr(self, "group_name"):
            await self.channel_layer.group_discard(self.group_name, self.channel_name)

    async def receive_json(self, content, **kwargs):
        body = content.get("body", "")
        try:
            message = await self._send(self.user, self.thread, body)
        except ChatError as exc:
            await self.send_json({"type": "error", "detail": str(exc)})
            return
        payload = await self._serialize(message)
        await self.channel_layer.group_send(
            self.group_name, {"type": "chat.message", "message": payload}
        )

    async def chat_message(self, event):
        # Per-delivery re-authorization (see ConversationConsumer): a member whose access
        # was revoked/blocked, whose activity was REMOVE'd/hidden, whose cohort changed, or
        # who was erased after connecting stops receiving live messages and is disconnected.
        if not await self._still_authorized():
            await self.close(code=4403)
            return
        await self.send_json({"type": "message", **event["message"]})

    @database_sync_to_async
    def _get_thread(self, thread_id):
        return Thread.objects.select_related("activity").filter(pk=thread_id).first()

    @database_sync_to_async
    def _can_access(self, user, thread):
        return can_access_thread(user, thread)

    @database_sync_to_async
    def _still_authorized(self) -> bool:
        from django.contrib.auth import get_user_model

        uid = getattr(self.user, "pk", None)
        user = get_user_model().objects.filter(pk=uid).first() if uid else None
        if user is None:
            return False
        thread = Thread.objects.select_related("activity").filter(pk=self.thread_id).first()
        return thread is not None and can_access_thread(user, thread)

    @database_sync_to_async
    def _send(self, user, thread, body):
        return send_message(user, thread, body)

    @database_sync_to_async
    def _serialize(self, message):
        return ChatMessageSerializer(message).data
