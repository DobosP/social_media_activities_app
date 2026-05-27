import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import (
    InvalidState,
    NotEligible,
    add_guardian,
    cast_vote,
    create_activity,
    request_to_join,
    voting_members,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child_activity(owner, *, guardian_accompanied=True, slug="g"):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Kids football",
        starts_at="2026-06-01T10:00Z",
        guardian_accompanied=guardian_accompanied,
    )


def test_only_child_activities_can_be_guardian_accompanied():
    adult = _adult("a1")
    with pytest.raises(InvalidState):
        _child_activity(adult, guardian_accompanied=True, slug="adult")


def test_owner_adds_verified_adult_guardian():
    child = _child("c1")
    activity = _child_activity(child, slug="ok")
    guardian = _adult("parent1")
    membership = add_guardian(child, activity, guardian)
    assert membership.role == Membership.Role.GUARDIAN
    assert membership.state == Membership.State.MEMBER


def test_guardian_must_be_verified_adult():
    child = _child("c2")
    activity = _child_activity(child, slug="ok2")
    another_child = _child("c2b")
    with pytest.raises(NotEligible):
        add_guardian(child, activity, another_child)


def test_guardian_not_allowed_when_flag_off():
    child = _child("c3")
    activity = _child_activity(child, guardian_accompanied=False, slug="off")
    guardian = _adult("parent3")
    with pytest.raises(InvalidState):
        add_guardian(child, activity, guardian)


def test_guardians_do_not_count_as_voters():
    child = _child("c4")
    activity = _child_activity(child, slug="vote")
    guardian = _adult("parent4")
    add_guardian(child, activity, guardian)
    # Only the child owner is a voting member; the guardian is excluded.
    assert voting_members(activity).count() == 1

    requester = _child("c4joiner")
    request = request_to_join(requester, activity)
    # Owner alone approving meets the 2/3 threshold (1/1), guardian vote not needed.
    cast_vote(child, request, True)
    request.refresh_from_db()
    assert request.state == Membership.State.MEMBER


def test_guardian_api_endpoint():
    child = _child("c5")
    activity = _child_activity(child, slug="api")
    guardian = _adult("parent5")
    client = APIClient()
    client.force_authenticate(child)
    resp = client.post(
        f"/api/social/activities/{activity.id}/guardians/",
        {"user_id": guardian.id},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.json()["role"] == Membership.Role.GUARDIAN
