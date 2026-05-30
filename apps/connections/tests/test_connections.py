"""Connections — the safety gates (cohort isolation, shared-activity precondition, blocking,
minors-off), the request/accept/decline/withdraw/remove lifecycle, search-ONLY discovery (no
suggestions feed), and the web round-trips. Connections are the discovery layer in front of the
existing E2EE messaging; they must never let an adult and a minor reach each other."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.connections import services as connections
from apps.connections.models import Connection
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _type(slug="conn-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="conn-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, slug="conn-bball"):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Pickup game",
        starts_at=timezone.now() + timedelta(days=1),
    )


def _join(activity, user, *, role=Membership.Role.MEMBER, state=Membership.State.MEMBER):
    return Membership.objects.create(activity=activity, user=user, role=role, state=state)


def _share(a, b, slug="conn-bball"):
    """Make a and b co-members of one activity (a owns it, b joins)."""
    act = _activity(a, slug)
    _join(act, b)
    return act


def _client(user):
    c = Client()
    c.force_login(user)
    return c


# --- the connect gate ----------------------------------------------------------------------


def test_can_connect_requires_shared_activity():
    a, b = _adult("a1"), _adult("b1")
    assert connections.can_connect(a, b) is False  # no shared activity yet
    _share(a, b)
    assert connections.can_connect(a, b) is True


def test_cannot_connect_across_cohorts():
    adult = _adult("ad1")
    child = _child("ch1")
    # Even if (hypothetically) co-membership existed, different cohorts can never connect.
    assert connections.can_connect(adult, child) is False
    assert connections.can_connect(child, adult) is False


def test_all_ages_connect_within_their_own_cohort():
    # "No matter age" (the SAFE version): every cohort can connect WITHIN itself. Two children
    # who shared a (child) activity can connect; the cross-age wall is tested separately.
    c1, c2 = _child("ch2"), _child("ch3")
    _share(c1, c2, slug="conn-kids")
    assert connections.is_enabled_for(c1) is True
    assert connections.can_connect(c1, c2) is True


def test_adult_minor_connection_remains_impossible():
    # The non-negotiable wall: an adult and a minor can NEVER connect, even with all cohorts
    # enabled and even if (hypothetically) they shared an activity — different cohort -> blocked.
    adult = _adult("xa1")
    child = _child("xc1")
    teen = User.objects.create_user(username="xt1", password=PW, display_name="Teen")
    apply_assurance(teen, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    assert connections.can_connect(adult, child) is False
    assert connections.can_connect(child, adult) is False
    assert connections.can_connect(adult, teen) is False
    assert connections.can_connect(teen, child) is False


def test_blocking_prevents_connect():
    a, b = _adult("a2"), _adult("b2")
    _share(a, b)
    block_user(a, b)
    assert connections.can_connect(a, b) is False
    assert connections.can_connect(b, a) is False


def test_cannot_connect_self():
    a = _adult("a3")
    assert connections.can_connect(a, a) is False


# --- lifecycle -----------------------------------------------------------------------------


def test_request_then_accept_connects():
    a, b = _adult("a4"), _adult("b4")
    _share(a, b)
    conn = connections.request_connection(a, b)
    assert conn.status == Connection.Status.PENDING
    assert not connections.are_connected(a, b)
    connections.respond_to_connection(b, conn, accept=True)
    assert connections.are_connected(a, b) and connections.are_connected(b, a)
    # the requester is notified of acceptance
    assert Notification.objects.filter(
        recipient=a, kind=Notification.Kind.CONNECTION_ACCEPTED
    ).exists()


def test_reciprocal_request_auto_accepts():
    a, b = _adult("a5"), _adult("b5")
    _share(a, b)
    connections.request_connection(a, b)
    # b asks a back while a->b is pending: auto-accept, no second handshake
    conn = connections.request_connection(b, a)
    assert conn.status == Connection.Status.ACCEPTED
    assert connections.are_connected(a, b)


def test_request_notifies_addressee():
    a, b = _adult("a6"), _adult("b6")
    _share(a, b)
    connections.request_connection(a, b)
    assert Notification.objects.filter(
        recipient=b, kind=Notification.Kind.CONNECTION_REQUEST
    ).exists()


def test_repeat_request_is_idempotent_and_does_not_respam():
    # A repeat request to someone with a request already pending must NOT fire a second notice
    # (anti-pestering / no notification-bait, and no end-run around a future decline).
    a, b = _adult("a6b"), _adult("b6b")
    _share(a, b)
    connections.request_connection(a, b)
    connections.request_connection(a, b)
    connections.request_connection(a, b)
    assert (
        Notification.objects.filter(recipient=b, kind=Notification.Kind.CONNECTION_REQUEST).count()
        == 1
    )


def test_request_rate_limited(settings):
    settings.CONNECTIONS_REQUEST_RATE_LIMIT = 1
    settings.CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS = 3600
    a = _adult("rl_a")
    b, c = _adult("rl_b"), _adult("rl_c")
    _share(a, b)
    _share(a, c, slug="conn-rl")
    connections.request_connection(a, b)  # first new request: ok
    with pytest.raises(connections.NotEligible):
        connections.request_connection(a, c)  # second distinct request: throttled


def test_guardian_is_not_a_peer_co_member():
    # A supervisory guardian's membership must NOT establish a shared activity for connections.
    from apps.social.models import Activity

    owner = _adult("gp_owner")
    g1 = _adult("gp_g1")
    g2 = _adult("gp_g2")
    act = _activity(owner, slug="conn-guard")
    act.status = Activity.Status.OPEN
    act.save(update_fields=["status"])
    _join(act, g1, role=Membership.Role.GUARDIAN)
    _join(act, g2, role=Membership.Role.GUARDIAN)
    # Two co-supervising guardians do NOT share a peer activity -> cannot connect via it.
    assert connections.shares_activity(g1, g2) is False
    assert connections.can_connect(g1, g2) is False
    # ...and a guardian is not surfaced as a connectable co-member in search.
    assert g1 not in connections.search_connectable(owner, "gp_g1")


def test_decline_withdraw_remove():
    a, b = _adult("a7"), _adult("b7")
    _share(a, b)
    conn = connections.request_connection(a, b)
    connections.respond_to_connection(b, conn, accept=False)
    conn.refresh_from_db()
    assert conn.status == Connection.Status.DECLINED
    assert not connections.are_connected(a, b)

    conn2 = connections.request_connection(a, b)  # can re-request after a decline
    connections.withdraw_request(a, conn2)
    conn2.refresh_from_db()
    assert conn2.status == Connection.Status.WITHDRAWN

    conn3 = connections.request_connection(a, b)
    connections.respond_to_connection(b, conn3, accept=True)
    connections.remove_connection(b, a)  # either side can sever
    assert not connections.are_connected(a, b)


def test_cannot_answer_someone_elses_request():
    a, b, c = _adult("a8"), _adult("b8"), _adult("c8")
    _share(a, b)
    conn = connections.request_connection(a, b)
    with pytest.raises(connections.NotEligible):
        connections.respond_to_connection(c, conn, accept=True)


def test_accept_regated_on_block_after_request():
    a, b = _adult("a9"), _adult("b9")
    _share(a, b)
    conn = connections.request_connection(a, b)
    block_user(b, a)  # b blocks a after requesting was received
    with pytest.raises(connections.NotEligible):
        connections.respond_to_connection(b, conn, accept=True)


# --- search-only discovery (no suggestions feed) -------------------------------------------


def test_search_requires_query_no_suggestions():
    a, b = _adult("alice_x"), _adult("bob_x")
    _share(a, b)
    assert connections.search_connectable(a, "") == []  # empty -> no browse-all feed
    assert connections.search_connectable(a, "a") == []  # too short
    names = [u.username for u in connections.search_connectable(a, "bob")]
    assert "bob_x" in names


def test_search_excludes_non_shared_and_blocked_and_connected():
    a = _adult("anchor")
    shared = _adult("shared_mate")
    _adult("stranger_mate")  # exists but shares no activity with `a`
    blocked = _adult("blocked_mate")
    _share(a, shared)
    _share(a, blocked, slug="conn-2")
    block_user(a, blocked)
    # stranger shares no activity with `a`
    found = {u.username for u in connections.search_connectable(a, "mate")}
    assert "shared_mate" in found
    assert "stranger_mate" not in found  # not a co-member
    assert "blocked_mate" not in found  # blocked
    # once connected, they drop out of search
    conn = connections.request_connection(a, shared)
    connections.respond_to_connection(shared, conn, accept=True)
    assert "shared_mate" not in {u.username for u in connections.search_connectable(a, "mate")}


# --- messaging integration -----------------------------------------------------------------


def test_open_conversation_requires_connection():
    a, b = _adult("a10"), _adult("b10")
    _share(a, b)
    with pytest.raises(connections.NotEligible):
        connections.open_conversation(a, b)
    conn = connections.request_connection(a, b)
    connections.respond_to_connection(b, conn, accept=True)
    conv = connections.open_conversation(a, b)
    assert conv is not None


# --- web round-trips -----------------------------------------------------------------------


def test_web_connect_accept_and_message_flow():
    a, b = _adult("webA"), _adult("webB")
    _share(a, b)
    # a sends a request via the web
    r = _client(a).post("/connections/request/", {"public_id": str(b.public_id)})
    assert r.status_code == 302
    conn = Connection.objects.get(requester=a, addressee=b)
    # b accepts
    r = _client(b).post(f"/connections/{conn.id}/respond/", {"accept": "1"})
    assert r.status_code == 302
    assert connections.are_connected(a, b)
    # a opens a chat -> redirected to messages
    r = _client(a).post("/connections/message/", {"public_id": str(b.public_id)})
    assert r.status_code == 302 and "/messages/" in r.url


def test_connections_page_search_only_renders():
    a, b = _adult("pagea"), _adult("pageb")
    _share(a, b)
    page = _client(a).get("/connections/?q=pageb").content.decode()
    assert "Search results" in page
    assert str(b.public_id) in page  # b surfaced as a connectable result (connect button)
    # ...and with no query there is NO suggestions feed.
    empty = _client(a).get("/connections/").content.decode()
    assert "Search results" not in empty


def test_connect_button_shows_on_co_member_row():
    a, b = _adult("btnA"), _adult("btnB")
    act = _share(a, b)
    page = _client(a).get(f"/activities/{act.id}/").content.decode()
    assert ">connect</button>" in page  # the co-member connect affordance


def test_connection_request_rejects_offsite_next():
    # Open-redirect guard: a crafted ?next to another host must fall back to /connections/.
    a, b = _adult("orA"), _adult("orB")
    _share(a, b)
    r = _client(a).post(
        "/connections/request/",
        {"public_id": str(b.public_id), "next": "https://evil.example.com/phish"},
    )
    assert r.status_code == 302
    assert "evil.example.com" not in r.url
    assert r.url.endswith("/connections/")
