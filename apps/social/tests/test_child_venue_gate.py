"""F9 — public meetup-place gate enforced in create_activity + can_join for CHILD activities.

The gate is OFF in the test settings by default (so the rest of the suite can use bare places);
each test here turns CHILD_PUBLIC_VENUES_ONLY ON via the pytest `settings` fixture.
"""

import zoneinfo
from datetime import datetime

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import ApprovedChildVenue, Place
from apps.social.services import (
    InvalidState,
    NotEligible,
    can_join,
    create_activity,
    request_to_join,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

TZ = zoneinfo.ZoneInfo("Europe/Bucharest")
PT = Point(23.6, 46.77, srid=4326)


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


def _place(slug, raw_tags=None, source=Place.Source.OSM):
    return Place.objects.create(
        name=f"V-{slug}", location=PT, source=source, raw_tags=raw_tags or {}
    )


def _type(slug):
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    return ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)


def _make(owner, place, slug):
    return create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Meetup",
        starts_at=datetime(2026, 6, 15, 10, 0, tzinfo=TZ),
    )


def test_child_blocked_at_unapproved_venue(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    child = _child("cv1")
    place = _place("bar1", {"amenity": "bar"})
    with pytest.raises(InvalidState):
        _make(child, place, "bar1")


def test_child_allowed_at_library(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    child = _child("cv2")
    place = _place("lib2", {"amenity": "library"})
    activity = _make(child, place, "lib2")
    assert activity.pk is not None


def test_child_allowed_at_staff_approved_place(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    child = _child("cv3")
    place = _place("bar3", {"amenity": "bar"})
    ApprovedChildVenue.objects.create(place=place)
    assert _make(child, place, "bar3").pk is not None


def test_adult_unaffected_by_gate(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    adult = _adult("cv4")
    place = _place("bar4", {"amenity": "bar"})
    assert _make(adult, place, "bar4").pk is not None  # gate is CHILD-only


def test_flag_off_allows_any_public_place(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = False
    child = _child("cv5")
    place = _place("bar5", {"amenity": "bar"})
    assert _make(child, place, "bar5").pk is not None


def test_can_join_blocks_child_activity_at_unapproved_venue(settings):
    # Build the activity with the gate OFF (bare venue), then turn it ON: a peer can_join must
    # then fail (covers a place that lost its classification after creation — defence in depth).
    settings.CHILD_PUBLIC_VENUES_ONLY = False
    owner = _child("cv6o")
    place = _place("bar6", {"amenity": "bar"})
    activity = _make(owner, place, "bar6")
    joiner = _child("cv6j")
    assert can_join(joiner, activity) is True  # gate off
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    assert can_join(joiner, activity) is False  # gate on, venue not approved
    with pytest.raises(NotEligible):
        request_to_join(joiner, activity)


def test_can_join_allows_child_activity_at_library(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    owner = _child("cv7o")
    place = _place("lib7", {"amenity": "library"})
    activity = _make(owner, place, "lib7")
    joiner = _child("cv7j")
    assert can_join(joiner, activity) is True
