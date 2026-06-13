"""F29 (web) — the supervised checkbox (CHILD-only), the live supervision chip + owner
add-supervisor flow, and the guarded toggle."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import active_supervisor_present, create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
PT = Point(23.6, 46.77, srid=4326)


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _type(slug):
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    return ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)


def _place(slug):
    return Place.objects.create(name=f"P-{slug}", location=PT, source=Place.Source.OSM)


def _supervised(owner, slug):
    return create_activity(
        owner,
        place=_place(slug),
        activity_type=_type(slug),
        title="Kids meetup",
        starts_at="2030-06-01T10:00Z",
        supervised=True,
    )


def test_supervised_checkbox_shown_for_child_only():
    child_body = _client(_child("w1")).get("/activities/new/").content.decode()
    assert "Require a supervising guardian" in child_body
    adult_body = _client(_adult("w1a")).get("/activities/new/").content.decode()
    assert "Require a supervising guardian" not in adult_body


def test_child_creates_supervised_activity_via_form():
    child = _child("w2")
    atype, place = _type("w2"), _place("w2")
    resp = _client(child).post(
        "/activities/new/",
        {
            "place": place.id,
            "activity_type": atype.id,
            "title": "Reading club",
            "description": "",
            "starts_at": "2030-01-01T10:00",
            "ends_at": "",
            "capacity": "",
            "supervised": "on",
        },
    )
    assert resp.status_code == 302
    from apps.social.models import Activity

    activity = Activity.objects.get(title="Reading club")
    assert activity.supervised is True


def test_detail_shows_supervisor_needed_then_supervised():
    owner = _child("w3o")
    activity = _supervised(owner, "w3")
    guardian = _adult("w3g")
    link_guardian(guardian, owner)  # the owner has a guardian to seat
    body = _client(owner).get(f"/activities/{activity.id}/").content.decode()
    assert "supervisor needed" in body
    assert f"/activities/{activity.id}/supervisor/add/" in body  # add-supervisor form present

    add = _client(owner).post(
        f"/activities/{activity.id}/supervisor/add/", {"user_id": guardian.id}
    )
    assert add.status_code == 302
    assert active_supervisor_present(activity) is True
    body2 = _client(owner).get(f"/activities/{activity.id}/").content.decode()
    assert "supervised" in body2


def test_add_supervisor_rejects_non_guardian():
    owner = _child("w4o")
    activity = _supervised(owner, "w4")
    not_a_guardian = _adult("w4x")  # adult, but NOT a registered guardian of the owner
    resp = _client(owner).post(
        f"/activities/{activity.id}/supervisor/add/", {"user_id": not_a_guardian.id}, follow=True
    )
    assert active_supervisor_present(activity) is False
    assert not Membership.objects.filter(
        activity=activity, user=not_a_guardian, role=Membership.Role.GUARDIAN
    ).exists()
    assert resp.status_code == 200


def test_non_owner_cannot_toggle_supervision():
    owner = _child("w5o")
    activity = _supervised(owner, "w5")
    other = _child("w5x")
    _client(other).post(f"/activities/{activity.id}/supervision/", {"supervised": "off"})
    activity.refresh_from_db()
    assert activity.supervised is True  # untouched by a non-owner


def test_drf_adult_cannot_create_supervised():
    from rest_framework.test import APIClient

    adult = _adult("w6")
    atype, place = _type("w6"), _place("w6")
    client = APIClient()
    client.force_authenticate(adult)
    resp = client.post(
        "/api/social/activities/",
        {
            "place": place.id,
            "activity_type": atype.id,
            "title": "x",
            "starts_at": "2030-01-01T10:00:00Z",
            "supervised": True,
        },
        format="json",
    )
    assert resp.status_code == 403, resp.content  # service rejects supervised for a non-CHILD owner
