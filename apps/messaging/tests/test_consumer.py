# Channels WebsocketCommunicator tests: ``transaction=True`` is required (the consumer reads the DB
# from a separate thread/connection and won't see data in the test's wrapping transaction). They
# deliberately do NOT use serialized_rollback — its post-flush deserialize collides
# nondeterministically on django_content_type, which made this suite flaky; the fixtures create
# their own data, so the seed-restore it provides isn't needed here.
import pytest
from channels.routing import URLRouter
from channels.testing import WebsocketCommunicator

from apps.messaging import services
from apps.messaging.routing import websocket_urlpatterns

from .conftest import keys_for, make_user

# Generous timeout so the async handshake/relay doesn't flake on a loaded CI runner
# (the WebsocketCommunicator default is only 1s).
WS_TIMEOUT = 10


def _communicator(conversation_id, user):
    communicator = WebsocketCommunicator(
        URLRouter(websocket_urlpatterns), f"/ws/messaging/{conversation_id}/"
    )
    communicator.scope["user"] = user
    return communicator


@pytest.mark.django_db(transaction=True)
async def test_active_members_relay_ciphertext():
    from channels.db import database_sync_to_async

    @database_sync_to_async
    def setup():
        a = make_user("ws_a")
        b = make_user("ws_b")
        conv = services.start_direct(a, b)
        services.accept_invite(b, conv)
        return a, b, conv, keys_for(conv)

    a, b, conv, recipient_keys = await setup()
    a_conn = _communicator(conv.id, a)
    b_conn = _communicator(conv.id, b)
    assert (await a_conn.connect(timeout=WS_TIMEOUT))[0] is True
    assert (await b_conn.connect(timeout=WS_TIMEOUT))[0] is True

    await a_conn.send_json_to(
        {"ciphertext": "Y2lwaGVy", "iv": "aXY=", "recipient_keys": recipient_keys}
    )

    echoed = await a_conn.receive_json_from(timeout=WS_TIMEOUT)
    received = await b_conn.receive_json_from(timeout=WS_TIMEOUT)
    assert echoed["type"] == "message"
    assert received["ciphertext"] == "Y2lwaGVy"
    # The broadcast carries every recipient's wrapped key; clients pick their own.
    recipients = {k["recipient_public_id"] for k in received["keys"]}
    assert recipients == {str(a.public_id), str(b.public_id)}

    await a_conn.disconnect()
    await b_conn.disconnect()


@pytest.mark.django_db(transaction=True)
async def test_invited_user_cannot_connect():
    from channels.db import database_sync_to_async

    @database_sync_to_async
    def setup():
        a = make_user("ws_inv_a")
        b = make_user("ws_inv_b")
        conv = services.start_direct(a, b)  # b stays INVITED
        return b, conv

    b, conv = await setup()
    conn = _communicator(conv.id, b)
    connected, _ = await conn.connect(timeout=WS_TIMEOUT)
    assert connected is False
    await conn.disconnect()


@pytest.mark.django_db(transaction=True)
async def test_outsider_connection_rejected():
    from channels.db import database_sync_to_async

    @database_sync_to_async
    def setup():
        a = make_user("ws_out_a")
        b = make_user("ws_out_b")
        outsider = make_user("ws_out_c")
        conv = services.start_direct(a, b)
        services.accept_invite(b, conv)
        return outsider, conv

    outsider, conv = await setup()
    conn = _communicator(conv.id, outsider)
    connected, _ = await conn.connect(timeout=WS_TIMEOUT)
    assert connected is False
    await conn.disconnect()
