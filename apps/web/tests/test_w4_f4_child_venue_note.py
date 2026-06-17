"""W4-F4: a read-only 'why this venue is child-approved' credit on the /wards/ manifest, making
the F9 child-venue gate legible to the responsible adult — only when the venue currently reads
'allowed' (silent omission otherwise), and only for CHILD wards (the gate is CHILD-only)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import ApprovedChildVenue, Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
CREDIT = "Child-approved venue"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _type(slug):
    cat, _ = ActivityCategory.objects.get_or_create(slug="w4f4-sport", defaults={"name": "Sport"})
    return ActivityType.objects.get_or_create(
        slug=f"w4f4-{slug}", defaults={"name": "Basketball", "category": cat}
    )[0]


def _place():
    return Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _meetup(owner, place, slug):
    return create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Kids meetup",
        starts_at=timezone.now() + timedelta(days=1),
    )


def test_wards_shows_staff_verified_credit():
    guardian = _user("f4g")
    ward = _user("f4w", AgeBand.UNDER_16, consented=True)
    owner = _user("f4o", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)
    place = _place()
    ApprovedChildVenue.objects.create(place=place)  # per-place staff approval -> "allowed"
    _member(_meetup(owner, place, "a"), ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert "Child-approved venue (staff-verified)" in body


def test_wards_omits_credit_for_unclassified_venue():
    guardian = _user("f4g2")
    ward = _user("f4w2", AgeBand.UNDER_16, consented=True)
    owner = _user("f4o2", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)
    _member(_meetup(owner, _place(), "b"), ward)  # no approval, no matching rule -> unknown

    body = _client(guardian).get("/wards/").content.decode()
    assert CREDIT not in body  # silent safe omission, never a false "approved" claim


def test_teen_ward_gets_no_child_venue_credit():
    guardian = _user("f4gt")
    ward = _user("f4wt", AgeBand.AGE_16_17)
    owner = _user("f4ot", AgeBand.AGE_16_17)
    link_guardian(guardian, ward)
    place = _place()
    ApprovedChildVenue.objects.create(place=place)
    _member(_meetup(owner, place, "c"), ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert CREDIT not in body  # the credit is CHILD-only (teens self-manage)
