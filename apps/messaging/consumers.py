from channels.db import database_sync_to_async
from channels.generic.websocket import AsyncJsonWebsocketConsumer

from . import services
from .models import Conversation
from .serializers import broadcast_payload


class ConversationConsumer(AsyncJsonWebsocketConsumer):
    """Per-conversation WebSocket room for live, end-to-end-encrypted delivery.

    The server only relays ciphertext: each inbound frame is validated and stored
    via `post_message` (which enforces membership, the recipient-key set, and rate
    limits), then the stored message — including every recipient's wrapped key — is
    fanned out to the room. Each client decrypts only the key addressed to it.
    """

    async def connect(self):
        self.user = self.scope.get("user")
        self.conversation_id = self.scope["url_route"]["kwargs"]["conversation_id"]
        self.group_name = f"conv_{self.conversation_id}"

        self.conversation = await self._get_conversation(self.conversation_id)
        if self.conversation is None or not await self._can_view(self.user, self.conversation):
            await self.close(code=4403)
            return

        await self.channel_layer.group_add(self.group_name, self.channel_name)
        await self.accept()

    async def disconnect(self, code):
        if hasattr(self, "group_name"):
            await self.channel_layer.group_discard(self.group_name, self.channel_name)

    async def receive_json(self, content, **kwargs):
        try:
            message = await self._post(content)
        except services.MessagingError as exc:
            await self.send_json({"type": "error", "detail": str(exc)})
            return
        payload = await self._broadcast_payload(message)
        await self.channel_layer.group_send(
            self.group_name, {"type": "conversation.message", "message": payload}
        )

    async def conversation_message(self, event):
        await self.send_json({"type": "message", **event["message"]})

    @database_sync_to_async
    def _get_conversation(self, conversation_id):
        return Conversation.objects.filter(pk=conversation_id).first()

    @database_sync_to_async
    def _can_view(self, user, conversation):
        return services.can_view(user, conversation)

    @database_sync_to_async
    def _post(self, content):
        return services.post_message(
            self.user,
            self.conversation,
            ciphertext=content.get("ciphertext", ""),
            iv=content.get("iv", ""),
            recipient_keys=content.get("recipient_keys") or [],
            algorithm=content.get("algorithm", "AES-GCM-256"),
        )

    @database_sync_to_async
    def _broadcast_payload(self, message):
        return broadcast_payload(message)
