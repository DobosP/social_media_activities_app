import pytest
from channels.routing import URLRouter
from channels.testing import WebsocketCommunicator

from apps.chat.routing import websocket_urlpatterns


def _communicator(thread_id, user):
    communicator = WebsocketCommunicator(URLRouter(websocket_urlpatterns), f"/ws/chat/{thread_id}/")
    communicator.scope["user"] = user
    return communicator


@pytest.mark.django_db(transaction=True, serialized_rollback=True)
async def test_member_can_connect_and_broadcast(thread, owner, member):
    owner_conn = _communicator(thread.id, owner)
    member_conn = _communicator(thread.id, member)
    assert (await owner_conn.connect())[0] is True
    assert (await member_conn.connect())[0] is True

    await owner_conn.send_json_to({"body": "hello room"})

    echoed = await owner_conn.receive_json_from()
    received = await member_conn.receive_json_from()
    assert echoed["type"] == "message"
    assert echoed["body"] == "hello room"
    assert received["body"] == "hello room"

    await owner_conn.disconnect()
    await member_conn.disconnect()


@pytest.mark.django_db(transaction=True, serialized_rollback=True)
async def test_outsider_connection_rejected(thread, outsider):
    conn = _communicator(thread.id, outsider)
    connected, _ = await conn.connect()
    assert connected is False
    await conn.disconnect()


@pytest.mark.django_db(transaction=True, serialized_rollback=True)
async def test_guardian_cannot_inject_via_socket(thread, owner):
    """A supervisory guardian may read the thread but the WebSocket write path must reject
    their message through the SAME gate as the web/DRF surfaces (no adult injecting into a
    children's thread). They get an error frame and nothing is persisted."""
    from channels.db import database_sync_to_async

    from apps.accounts.models import AgeBand, User
    from apps.social.models import Membership

    @database_sync_to_async
    def make_guardian():
        from django.utils import timezone

        g = User.objects.create_user(username="grd", password="pw", age_band=AgeBand.ADULT)
        g.recompute_cohort()
        g.is_identity_verified = True
        g.identity_verified_at = timezone.now()
        g.save()
        Membership.objects.create(
            activity=thread.activity,
            user=g,
            role=Membership.Role.GUARDIAN,
            state=Membership.State.MEMBER,
        )
        return g

    @database_sync_to_async
    def post_count():
        return thread.posts.count()

    guardian = await make_guardian()
    before = await post_count()
    conn = _communicator(thread.id, guardian)
    assert (await conn.connect())[0] is True  # guardians may READ
    await conn.send_json_to({"body": "I am the parent"})
    frame = await conn.receive_json_from()
    assert frame["type"] == "error"  # ...but not WRITE
    assert await post_count() == before  # nothing persisted
    await conn.disconnect()
