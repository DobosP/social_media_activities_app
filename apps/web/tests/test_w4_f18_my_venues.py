"""W4-F18: a self-only data-quality digest — for the meetups the viewer is going to, flag venues
that read reported-closed / unverified-hours / pending-correction. A page, never a job."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceCorrection
from apps.places.services import venue_quality_flags
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="w4f18-sport", defaults={"name": "Sport"})
    return ActivityType.objects.get_or_create(
        slug="w4f18-bball", defaults={"name": "Basketball", "category": cat}
    )[0]


def _place(name):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _activity(owner, place):
    return create_activity(
        owner,
        place=place,
        activity_type=_type(),
        title="Game",
        starts_at=timezone.now() + timedelta(days=2),
    )


def _pending_correction(place, proposer):
    return PlaceCorrection.objects.create(
        place=place,
        proposer=proposer,
        field=PlaceCorrection.Field.NAME,
        proposed_value="Corrected name",
        status=PlaceCorrection.Status.PENDING,
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def test_venue_quality_flags_pending_correction():
    user = _user("f18svc")
    place = _place("Service Hall")
    assert venue_quality_flags(place) == []  # clean
    _pending_correction(place, user)
    assert "correction_pending" in venue_quality_flags(place)


def test_my_venues_flags_a_pending_correction():
    user = _user("f18a")
    place = _place("Library")
    _activity(user, place)  # user is owner-member -> the meetup is on their list
    _pending_correction(place, user)
    body = _client(user).get("/my-venues/").content.decode()
    assert "Library" in body
    assert "a correction is pending" in body


def test_my_venues_omits_clean_venues():
    user = _user("f18b")
    place = _place("Clean Hall")
    _activity(user, place)
    body = _client(user).get("/my-venues/").content.decode()
    assert "Clean Hall" not in body  # nothing to flag
    assert "all look fine" in body  # the empty-state message


def test_my_venues_is_self_scoped():
    viewer = _user("f18c")
    other = _user("f18c_other")
    other_place = _place("Other Hall")
    _activity(other, other_place)  # OTHER user's meetup
    _pending_correction(other_place, other)
    body = _client(viewer).get("/my-venues/").content.decode()
    assert "Other Hall" not in body  # never another member's meetup venue
