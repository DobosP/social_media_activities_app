import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.notifications.services import mark_all_read, notify, unread_count
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import cast_vote, create_activity, request_to_join
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner, slug="n"):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"n-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"n-{slug}-bball", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Pickup game", starts_at="2026-06-01T10:00Z"
    )


def test_notify_and_unread_count():
    user = _adult("u1")
    notify(user, Notification.Kind.SYSTEM, "Hello", body="welcome")
    assert unread_count(user) == 1


def test_owner_notified_on_join_request():
    owner = _adult("owner")
    activity = _activity(owner, "req")
    joiner = _adult("joiner")
    request_to_join(joiner, activity)
    assert Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.JOIN_REQUESTED
    ).exists()


def test_requester_notified_on_admission():
    owner = _adult("owner2")
    activity = _activity(owner, "adm")  # owner is the only voting member → 1/1 >= 2/3
    joiner = _adult("joiner2")
    membership = request_to_join(joiner, activity)
    cast_vote(owner, membership, True)
    membership.refresh_from_db()
    assert membership.state == Membership.State.MEMBER
    assert Notification.objects.filter(
        recipient=joiner, kind=Notification.Kind.JOIN_APPROVED
    ).exists()


def test_api_list_mark_read_and_read_all():
    user = _adult("api")
    notify(user, Notification.Kind.SYSTEM, "One")
    notify(user, Notification.Kind.SYSTEM, "Two")
    client = APIClient()
    client.force_authenticate(user)

    listing = client.get("/api/notifications/").json()
    assert listing["unread_count"] == 2
    assert len(listing["results"]) == 2

    first_id = listing["results"][0]["id"]
    assert client.post(f"/api/notifications/{first_id}/read/").status_code == 200
    assert client.get("/api/notifications/?unread=true").json()["unread_count"] == 1

    assert client.post("/api/notifications/read-all/").json()["marked_read"] == 1
    assert unread_count(user) == 0


def test_only_own_notifications_visible():
    owner = _adult("owns")
    other = _adult("other")
    notify(owner, Notification.Kind.SYSTEM, "private")
    client = APIClient()
    client.force_authenticate(other)
    assert client.get("/api/notifications/").json()["results"] == []
    # And one can't mark someone else's as read.
    n = Notification.objects.get(recipient=owner)
    assert client.post(f"/api/notifications/{n.id}/read/").status_code == 404


def test_mark_all_read_helper():
    user = _adult("mah")
    notify(user, Notification.Kind.SYSTEM, "a")
    notify(user, Notification.Kind.SYSTEM, "b")
    assert mark_all_read(user) == 2
