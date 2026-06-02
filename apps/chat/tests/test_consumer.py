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
async def test_group_thread_socket_uses_same_gate():
    """A GROUP thread routes through the SAME consumer gate as an activity thread (the consumer now
    passes thread.owner_object everywhere): a member connects + broadcasts; a non-member and a
    cross-cohort (CHILD) user are both rejected at connect (4403)."""
    from channels.db import database_sync_to_async

    from apps.accounts.models import AgeBand
    from apps.communities.models import Area
    from apps.social import services as social
    from apps.social.tests.conftest import make_user
    from apps.taxonomy.models import ActivityCategory, ActivityType

    @database_sync_to_async
    def setup():
        staff = make_user("gsock_owner", AgeBand.ADULT)
        staff.is_staff = True
        staff.save(update_fields=["is_staff"])
        cat, _ = ActivityCategory.objects.get_or_create(
            slug="gsock-sport", defaults={"name": "Sport"}
        )
        at, _ = ActivityType.objects.get_or_create(
            slug="gsock-bball", defaults={"name": "Basketball", "category": cat}
        )
        area = Area.objects.create(city="Sock City", slug="sock-city", name="Sock City")
        group = social.create_group(staff, area=area, title="Sock Group", activity_type=at)
        member = make_user("gsock_member", AgeBand.ADULT)
        social.join_group(member, group.id)
        outsider = make_user("gsock_out", AgeBand.ADULT)
        child = make_user("gsock_child", AgeBand.UNDER_16, consented=True)
        return group.thread.id, member, outsider, child

    thread_id, member, outsider, child = await setup()

    mc = _communicator(thread_id, member)
    assert (await mc.connect())[0] is True
    await mc.send_json_to({"body": "hi group"})
    echoed = await mc.receive_json_from()
    assert echoed["type"] == "message" and echoed["body"] == "hi group"
    await mc.disconnect()

    oc = _communicator(thread_id, outsider)
    assert (await oc.connect())[0] is False  # non-member rejected
    await oc.disconnect()

    cc = _communicator(thread_id, child)
    assert (await cc.connect())[0] is False  # cross-cohort (CHILD on ADULT group) rejected
    await cc.disconnect()


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
