from channels.db import database_sync_to_async
from channels.generic.websocket import AsyncJsonWebsocketConsumer

from apps.social.models import Thread
from apps.social.services import (
    SocialError,
    can_read_thread,
    post_to_thread_realtime,
    typing_identity,
)


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
        # Transient 'typing' signal: emit-and-forget over the group. It is PURE TRANSPORT — no
        # Post, no DB row, nothing stored — so it can never become a presence record. The gate
        # (typing_identity) re-derives, on FRESH state, that the sender is a non-guardian member of
        # a thread that isn't a minor-cohort announcement-only group, mirroring the write gate; a
        # guardian or a muted minor-group member emits nothing. The handler self-excludes the typer.
        if content.get("type") == "typing":
            # PRECONDITION: _still_authorized() above already closed 4403 on a revoked/blocked/
            # cohort-changed/erased sender, so the gate is enforced. The emit itself is pure
            # best-effort transport — a transient channel-layer/DB hiccup on a keystroke-frequency
            # 'typing' frame must be a silent no-op, never tear down an otherwise-healthy socket
            # (mirrors broadcast_post / broadcast_reaction, which are likewise wrapped).
            try:
                info = await self._typing_identity()
                if info is not None:
                    await self.channel_layer.group_send(
                        self.group_name,
                        {
                            "type": "chat.typing",
                            "sender": self.channel_name,
                            "author_id": info["author_id"],
                            "author": info["author"],
                        },
                    )
            except Exception:  # noqa: BLE001 — typing is best-effort; never break the socket
                pass
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

    async def chat_reaction(self, event):
        # A post's distinct reaction set changed (anonymous, COUNTLESS — no count, no who). Same
        # per-delivery re-auth as chat_message so a revoked member stops receiving updates.
        if not await self._still_authorized():
            await self.close(code=4403)
            return
        await self.send_json({"type": "reaction", **event["message"]})

    async def chat_typing(self, event):
        # Never echo a typer their own signal. Re-auth every delivery, so a member whose access was
        # revoked since connecting stops seeing peers type. Transport-only; the client shows an
        # ephemeral hint that is deliberately NOT announced to screen readers (peer presence is
        # silent, matching the live-region rule for ordinary peer messages).
        if event.get("sender") == self.channel_name:
            return
        if not await self._still_authorized():
            await self.close(code=4403)
            return
        await self.send_json(
            {"type": "typing", "author_id": event["author_id"], "author": event["author"]}
        )

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
    def _typing_identity(self):
        # Resolve the typing gate on FRESH state (reloaded user + thread, like _still_authorized):
        # a guardian, a non-member, or a muted minor-group member gets None and emits nothing.
        from django.contrib.auth import get_user_model

        uid = getattr(self.user, "pk", None)
        user = get_user_model().objects.filter(pk=uid).first() if uid else None
        if user is None:
            return None
        thread = (
            Thread.objects.select_related("activity", "group").filter(pk=self.thread_id).first()
        )
        if thread is None:
            return None
        return typing_identity(user, thread.owner_object)

    @database_sync_to_async
    def _persist(self, body, reply_to_id):
        return post_to_thread_realtime(
            self.user, self.thread.owner_object, body, reply_to_id=reply_to_id
        )
