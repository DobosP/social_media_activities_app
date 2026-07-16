# These are Channels WebsocketCommunicator tests, so they need ``transaction=True``: the consumer
# reads the DB from a separate thread/connection (via database_sync_to_async) and would not see data
# created inside the test's wrapping transaction. They deliberately do NOT use serialized_rollback —
# its post-flush ``deserialize_db_from_string`` collides nondeterministically on django_content_type
# (e.g. "admin, logentry already exists"), which is what made this suite flaky. The fixtures create
# everything they need via get_or_create, so the seed-restore that serialized_rollback provides
# isn't required here.
import pytest
from channels.db import database_sync_to_async
from channels.routing import URLRouter
from channels.testing import WebsocketCommunicator
from django.utils import timezone

from apps.chat.routing import websocket_urlpatterns


def _communicator(thread_id, user):
    communicator = WebsocketCommunicator(URLRouter(websocket_urlpatterns), f"/ws/chat/{thread_id}/")
    communicator.scope["user"] = user
    return communicator


@pytest.mark.django_db(transaction=True)
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


@pytest.mark.django_db(transaction=True)
async def test_outsider_connection_rejected(thread, outsider):
    conn = _communicator(thread.id, outsider)
    connected, _ = await conn.connect()
    assert connected is False
    await conn.disconnect()


@pytest.mark.django_db(transaction=True)
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


@pytest.mark.django_db(transaction=True)
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


@pytest.mark.django_db(transaction=True)
async def test_live_message_carries_server_rendered_body_html(thread, owner, member):
    """A broadcast post carries body_html — the SAME safe HTML the no-JS page renders (peer
    @mention highlight + the markdown subset) — so a live-appended post is first-class, never a
    plain-text bubble that changes appearance on reload."""
    owner_conn = _communicator(thread.id, owner)
    member_conn = _communicator(thread.id, member)
    assert (await owner_conn.connect())[0] is True
    assert (await member_conn.connect())[0] is True

    await owner_conn.send_json_to({"body": "hi @member **welcome**"})
    echoed = await owner_conn.receive_json_from()
    await member_conn.receive_json_from()
    assert echoed["type"] == "message"
    assert '<span class="mention">@member</span>' in echoed["body_html"]
    assert "<strong>welcome</strong>" in echoed["body_html"]

    await owner_conn.disconnect()
    await member_conn.disconnect()


@pytest.mark.django_db(transaction=True)
async def test_typing_signal_reaches_peers_but_not_the_typer(thread, owner, member):
    """A transient typing signal fans out to OTHER members and never echoes back to the typer, and
    persists NOTHING (pure transport — never a presence record)."""
    from apps.social.models import Post

    owner_conn = _communicator(thread.id, owner)
    member_conn = _communicator(thread.id, member)
    assert (await owner_conn.connect())[0] is True
    assert (await member_conn.connect())[0] is True

    await owner_conn.send_json_to({"type": "typing"})
    frame = await member_conn.receive_json_from()
    assert frame["type"] == "typing" and frame["author_id"] == owner.id
    assert await owner_conn.receive_nothing()  # the typer never sees their own typing

    @database_sync_to_async
    def post_count():
        return Post.objects.filter(thread=thread).count()

    assert await post_count() == 0  # typing wrote nothing to the durable stream

    await owner_conn.disconnect()
    await member_conn.disconnect()


@pytest.mark.django_db(transaction=True)
async def test_guardian_typing_is_suppressed(thread, owner, member):
    """A supervisory guardian may READ, but their typing signal reaches nobody — no adult presence
    leaks into a children's thread (mirrors the write-gate guardian rejection)."""
    from apps.accounts.models import AgeBand, User
    from apps.social.models import Membership

    @database_sync_to_async
    def make_guardian():
        g = User.objects.create_user(username="grd2", password="pw", age_band=AgeBand.ADULT)
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

    guardian = await make_guardian()
    g_conn = _communicator(thread.id, guardian)
    owner_conn = _communicator(thread.id, owner)
    member_conn = _communicator(thread.id, member)
    assert (await g_conn.connect())[0] is True  # a guardian may read
    assert (await owner_conn.connect())[0] is True
    assert (await member_conn.connect())[0] is True

    await g_conn.send_json_to({"type": "typing"})
    assert await owner_conn.receive_nothing()  # nobody is told the guardian is typing
    assert await member_conn.receive_nothing()

    await g_conn.disconnect()
    await owner_conn.disconnect()
    await member_conn.disconnect()


@pytest.mark.django_db(transaction=True)
async def test_reaction_toggle_emits_no_websocket_frame(thread, owner, member):
    """ADR-0029 removed the live per-reaction broadcast (the distinct-facet set surfaced at n=1 — a
    small-roster timing leak; the aggregate now lives only in the batched sentiment footer). A
    reaction toggle must therefore emit NO websocket frame to connected members."""
    from apps.social import services as social
    from apps.social.models import Post

    owner_conn = _communicator(thread.id, owner)
    member_conn = _communicator(thread.id, member)
    assert (await owner_conn.connect())[0] is True
    assert (await member_conn.connect())[0] is True

    await owner_conn.send_json_to({"body": "hi"})
    echoed = await owner_conn.receive_json_from()
    await member_conn.receive_json_from()
    post_id = echoed["id"]

    facet = social.allowed_reactions()[0]

    @database_sync_to_async
    def react():
        return social.toggle_reaction(member, Post.objects.get(pk=post_id), facet)

    assert await react() is True
    # No 'reaction' frame (or any frame) reaches either connected member.
    assert await owner_conn.receive_nothing()
    assert await member_conn.receive_nothing()

    await owner_conn.disconnect()
    await member_conn.disconnect()


@pytest.mark.django_db(transaction=True)
async def test_typing_emit_failure_does_not_tear_down_socket(thread, owner, monkeypatch):
    """A transient failure while emitting the transport-only 'typing' signal must be a silent no-op,
    never a socket teardown — typing is best-effort, like broadcast_post / broadcast_reaction."""
    import apps.chat.consumers as consumers_mod

    def boom(*a, **k):
        raise RuntimeError("transient channel-layer / DB hiccup")

    monkeypatch.setattr(consumers_mod, "typing_identity", boom)

    conn = _communicator(thread.id, owner)
    assert (await conn.connect())[0] is True
    await conn.send_json_to({"type": "typing"})  # the emit raises internally -> swallowed
    # The socket is still healthy: a normal durable message still round-trips.
    await conn.send_json_to({"body": "still here"})
    echoed = await conn.receive_json_from()
    assert echoed["type"] == "message" and echoed["body"] == "still here"
    await conn.disconnect()
