import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import can_join, create_activity, open_positions, request_to_join
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _activity(owner, *, capacity=None, slug="cap"):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"c-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"a-{slug}", name="Basketball", category=cat)
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Pickup game (open positions)",
        starts_at="2026-06-01T10:00Z",
        capacity=capacity,
    )


def test_open_positions_and_capacity_block():
    owner = _adult("o1")
    activity = _activity(owner, capacity=2, slug="full")  # owner takes 1 of 2
    assert open_positions(activity) == 1

    joiner = _adult("j1")
    m = request_to_join(joiner, activity)
    m.state = Membership.State.MEMBER
    m.save(update_fields=["state"])
    assert open_positions(activity) == 0

    # No positions left — the next person can't join.
    latecomer = _adult("j2")
    assert can_join(latecomer, activity) is False


def test_uncapped_activity_has_null_open_positions():
    activity = _activity(_adult("o2"), capacity=None, slug="uncapped")
    assert open_positions(activity) is None


def test_guardian_joins_on_behalf_of_ward():
    # A child owns a child-cohort activity with an open position.
    child_owner = _child("kidowner")
    activity = _activity(child_owner, capacity=5, slug="behalf")

    ward = _child("ward")
    guardian = _adult("parent")
    link_guardian(guardian, ward)

    client = APIClient()
    client.force_authenticate(guardian)  # guardian is logged in, acting for the child
    resp = client.post(
        f"/api/social/activities/{activity.id}/join/",
        {"on_behalf_of": str(ward.public_id)},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    # The membership belongs to the WARD, not the guardian.
    assert Membership.objects.filter(activity=activity, user=ward).exists()
    assert not Membership.objects.filter(activity=activity, user=guardian).exists()


def test_cannot_act_on_behalf_without_guardianship():
    child_owner = _child("kidowner2")
    activity = _activity(child_owner, slug="nobehalf")
    ward = _child("ward2")
    stranger = _adult("notparent")

    client = APIClient()
    client.force_authenticate(stranger)
    resp = client.post(
        f"/api/social/activities/{activity.id}/join/",
        {"on_behalf_of": str(ward.public_id)},
        format="json",
    )
    assert resp.status_code == 403
