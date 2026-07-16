# ADR-0026 live-chat media parity: the group broadcast carries attachment IDs only, and each
# member's consumer resolves them per-viewer (signed URLs are viewer-bound, so they can never
# ride a group payload). Same transaction=True posture as test_consumer.py.
import pytest
from channels.db import database_sync_to_async
from channels.routing import URLRouter
from channels.testing import WebsocketCommunicator

from apps.chat.routing import websocket_urlpatterns
from apps.media.models import Attachment


def _communicator(thread_id, user):
    communicator = WebsocketCommunicator(URLRouter(websocket_urlpatterns), f"/ws/chat/{thread_id}/")
    communicator.scope["user"] = user
    return communicator


def _png(size=(64, 64)):
    from io import BytesIO

    from PIL import Image

    out = BytesIO()
    Image.new("RGB", size, (30, 120, 200)).save(out, format="PNG")
    return out.getvalue()


@database_sync_to_async
def _post_with_image(owner, thread):
    # One transaction like the real web upload path: the on_commit broadcast fires only after
    # BOTH the post and its attachment exist, so the live payload carries the attachment id.
    from django.db import transaction

    from apps.media.services import attach_to_post
    from apps.social.services import post_to_thread

    with transaction.atomic():
        post = post_to_thread(owner, thread.activity, "look at this")
        attach_to_post(owner, post, filename="pic.png", data=_png())
    return post


@pytest.mark.django_db(transaction=True)
async def test_live_message_carries_per_viewer_attachment_urls(thread, owner, member):
    owner_conn = _communicator(thread.id, owner)
    member_conn = _communicator(thread.id, member)
    assert (await owner_conn.connect())[0] is True
    assert (await member_conn.connect())[0] is True

    await _post_with_image(owner, thread)

    got_owner = await owner_conn.receive_json_from()
    got_member = await member_conn.receive_json_from()
    for got in (got_owner, got_member):
        assert got["type"] == "message"
        assert len(got["attachments"]) == 1
        att = got["attachments"][0]
        assert att["kind"] == "image"
        assert att["url"].startswith("/api/media/attachment/")
        assert att["processing"] is False and att["expired"] is False
    # Tokens are viewer-bound: the same attachment yields DIFFERENT URLs per member.
    assert got_owner["attachments"][0]["url"] != got_member["attachments"][0]["url"]
    # The raw id list never reaches the client — only the resolved per-viewer entries.
    assert "attachment_ids" not in got_owner

    await owner_conn.disconnect()
    await member_conn.disconnect()


@database_sync_to_async
def _pending_video_post(owner, thread):
    from django.db import transaction

    from apps.social.services import post_to_thread

    with transaction.atomic():
        post = post_to_thread(owner, thread.activity, "clip incoming")
        _make_pending_video(post, owner)
    return post


def _make_pending_video(post, owner):
    Attachment.objects.create(
        post=post,
        uploader=owner,
        kind=Attachment.Kind.VIDEO,
        status=Attachment.Status.PENDING,
        storage_key="",
        content_type="video/mp4",
        source_storage_key="video-src/x.mp4",
    )


@database_sync_to_async
def _mark_video_ready_and_update(post):
    from apps.social.services import broadcast_attachment_update

    att = post.attachments.get()
    att.status = Attachment.Status.READY
    att.storage_key = "videos/x.mp4"
    att.poster_storage_key = "video-posters/x.webp"
    att.poster_content_type = "image/webp"
    att.source_storage_key = ""
    att.save()
    broadcast_attachment_update(post)


@pytest.mark.django_db(transaction=True)
async def test_video_processing_placeholder_then_live_ready_update(thread, owner, member):
    member_conn = _communicator(thread.id, member)
    assert (await member_conn.connect())[0] is True

    post = await _pending_video_post(owner, thread)
    got = await member_conn.receive_json_from()
    assert got["type"] == "message"
    assert got["attachments"][0]["processing"] is True
    assert got["attachments"][0]["url"] == ""  # withheld: no bytes are reachable while pending

    await _mark_video_ready_and_update(post)
    update = await member_conn.receive_json_from()
    assert update["type"] == "attachments"
    assert update["post_id"] == post.id
    att = update["attachments"][0]
    assert att["kind"] == "video" and att["processing"] is False
    assert att["url"].startswith("/api/media/attachment/")
    assert att["poster_url"].startswith("/api/media/attachment/")

    await member_conn.disconnect()
