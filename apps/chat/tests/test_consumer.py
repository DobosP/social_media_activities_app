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
