from channels.db import database_sync_to_async
from channels.generic.websocket import AsyncJsonWebsocketConsumer

from apps.social.models import Thread
from apps.social.services import SocialError, can_read_thread, post_to_thread_realtime


class ChatConsumer(AsyncJsonWebsocketConsumer):
    """Per-thread WebSocket room, private to the activity's members.

    The socket is PURE live delivery over the durable ``social.Post`` stream — it is not a
    second store. An inbound message is persisted through the one hardened write path
    (``post_to_thread_realtime`` -> ``post_to_thread``); the resulting Post's
    ``transaction.on_commit`` broadcast fans it out to this group, so the consumer never
    re-sends. Membership + cohort are checked on connect, on every inbound message, and on
    every delivery, all through the single ``can_read_thread`` gate.
    """

    async def connect(self):
        self.user = self.scope.get("user")
        self.thread_id = self.scope["url_route"]["kwargs"]["thread_id"]
        self.group_name = f"chat_{self.thread_id}"

        self.thread = await self._get_thread(self.thread_id)
        if self.thread is None or not await self._can_access():
            await self.close(code=4403)
            return

        await self.channel_layer.group_add(self.group_name, self.channel_name)
        await self.accept()

    async def disconnect(self, code):
        if hasattr(self, "group_name"):
            await self.channel_layer.group_discard(self.group_name, self.channel_name)

    async def receive_json(self, content, **kwargs):
        # Re-authorize the SENDER against FRESH state before persisting: a banned/revoked/
        # cohort-changed/erased member with an open socket must not inject via the cached scope
        # user. The write itself goes through the full union gate in post_to_thread; the live
        # fan-out happens from that Post's on_commit broadcast (not from here).
        if not await self._still_authorized():
            await self.close(code=4403)
            return
        body = content.get("body", "")
        # Coerce the untrusted reply_to to an int-or-None at the boundary so a bad value can
        # never raise an uncaught ValueError that tears down the socket (the service also guards).
        raw = content.get("reply_to")
        reply_to_id = (
            raw
            if isinstance(raw, int)
            else (int(raw) if isinstance(raw, str) and raw.isdigit() else None)
        )
        try:
            await self._persist(body, reply_to_id)
        except SocialError as exc:
            await self.send_json({"type": "error", "detail": str(exc)})

    async def chat_message(self, event):
        # Per-delivery re-authorization: a member whose access was revoked/blocked, whose
        # activity was REMOVE'd/hidden, whose cohort changed, or who was erased after
        # connecting stops receiving live messages and is disconnected.
        if not await self._still_authorized():
            await self.close(code=4403)
            return
        await self.send_json({"type": "message", **event["message"]})

    @database_sync_to_async
    def _get_thread(self, thread_id):
        # Load BOTH owners so thread.owner_object resolves without an extra query whether this is
        # an activity thread or a group thread.
        return Thread.objects.select_related("activity", "group").filter(pk=thread_id).first()

    @database_sync_to_async
    def _can_access(self) -> bool:
        return can_read_thread(self.user, self.thread.owner_object)

    @database_sync_to_async
    def _still_authorized(self) -> bool:
        from django.contrib.auth import get_user_model

        uid = getattr(self.user, "pk", None)
        user = get_user_model().objects.filter(pk=uid).first() if uid else None
        if user is None:
            return False
        thread = (
            Thread.objects.select_related("activity", "group").filter(pk=self.thread_id).first()
        )
        return thread is not None and can_read_thread(user, thread.owner_object)

    @database_sync_to_async
    def _persist(self, body, reply_to_id):
        return post_to_thread_realtime(
            self.user, self.thread.owner_object, body, reply_to_id=reply_to_id
        )
