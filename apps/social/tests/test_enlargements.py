import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social.models import Membership
from apps.social.services import (
    InvalidState,
    create_activity,
    leave_activity,
    request_to_join,
    visible_activities,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner, slug="en"):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"at-{slug}", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2026-06-01T10:00Z"
    )


def test_blocked_owner_activity_hidden_from_discovery():
    owner = _user("owner")
    activity = _activity(owner)
    viewer = _user("viewer")
    assert activity in visible_activities(viewer)

    block_user(viewer, owner)
    assert activity not in visible_activities(viewer)


def test_leave_activity_marks_removed():
    owner = _user("o")
    activity = _activity(owner, "leave")
    member = _user("m")
    m = request_to_join(member, activity)
    m.state = Membership.State.MEMBER
    m.save(update_fields=["state"])

    left = leave_activity(member, activity)
    assert left.state == Membership.State.REMOVED


def test_owner_cannot_leave():
    owner = _user("o2")
    activity = _activity(owner, "noleave")
    with pytest.raises(InvalidState):
        leave_activity(owner, activity)


def test_mine_endpoint_lists_active_memberships():
    owner = _user("o3")
    activity = _activity(owner, "mine")
    client = APIClient()
    client.force_authenticate(owner)
    resp = client.get("/api/social/activities/mine/")
    assert resp.status_code == 200
    assert len(resp.json()) == 1
    assert resp.json()[0]["activity"] == activity.id


def test_leave_endpoint():
    owner = _user("o4")
    activity = _activity(owner, "leaveapi")
    member = _user("m4")
    m = request_to_join(member, activity)
    m.state = Membership.State.MEMBER
    m.save(update_fields=["state"])

    client = APIClient()
    client.force_authenticate(member)
    resp = client.post(f"/api/social/activities/{activity.id}/leave/")
    assert resp.status_code == 200
    assert resp.json()["state"] == Membership.State.REMOVED
