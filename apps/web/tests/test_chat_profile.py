"""Profile shows the user's connections, and the messages page hands the client its
connections (so the chat can offer quick "start a chat" shortcuts). The chat crypto/UI is JS;
these cover the server-side data flow + gates."""

import json
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.connections import services as connections
from apps.connections.models import Connection
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _connected(a, b):
    """Make a and b connected via a shared activity + accepted request."""
    cat, _ = ActivityCategory.objects.get_or_create(slug="cp-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="cp-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    act = social.create_activity(
        a, place=place, activity_type=t, title="Game", starts_at=timezone.now() + timedelta(days=1)
    )
    Membership.objects.create(
        activity=act, user=b, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    conn = connections.request_connection(a, b)
    connections.respond_to_connection(b, conn, accept=True)
    assert conn.status == Connection.Status.ACCEPTED


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def test_profile_lists_connections_with_message_button():
    a, b = _adult("cp_a"), _adult("cp_b")
    _connected(a, b)
    page = _client(a).get("/profile/").content.decode()
    assert "Connections" in page
    assert b.display_name in page  # the connection's display name is shown
    assert "/connections/message/" in page  # a Message button per connection


def test_profile_no_connections_shows_cta():
    a = _adult("cp_solo")
    page = _client(a).get("/profile/").content.decode()
    assert "No connections yet" in page


def test_messages_page_hands_client_its_connections():
    a, b = _adult("cp_m1"), _adult("cp_m2")
    _connected(a, b)
    page = _client(a).get("/messages/").content.decode()
    # The embedded config JSON drives the "chat with a connection" shortcuts.
    raw = page.split('id="mz-config"', 1)[1].split(">", 1)[1].split("</script>", 1)[0]
    cfg = json.loads(raw)
    usernames = [c["username"] for c in cfg["connections"]]
    assert b.username in usernames
    assert cfg["me"]["username"] == a.username
    # The inbox now exposes the low-friction start path in the main sidebar, not behind
    # an advanced disclosure.
    assert "Start a chat" in page
    assert "Username or group" in page
    assert "They must accept before they can read your messages." in page
    assert "Device and guardian options" in page


def test_messages_page_connections_are_empty_without_any():
    a = _adult("cp_m3")
    page = _client(a).get("/messages/").content.decode()
    raw = page.split('id="mz-config"', 1)[1].split(">", 1)[1].split("</script>", 1)[0]
    cfg = json.loads(raw)
    assert cfg["connections"] == []
    assert "Use Start a chat for a username" in page
