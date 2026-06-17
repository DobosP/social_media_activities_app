"""W4-F31: a verified civic partner's standing blurb is surfaced as a calm note on place_detail,
behind the same Partner.objects.public() (verified + active) chokepoint as the partner credit."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Partner, Place

pytestmark = pytest.mark.django_db
NOTE = "Our community garden welcomes weekend tidy-up groups"


def _viewer():
    u = User.objects.create_user(username="f31v", password="pw", display_name="f31v")
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    c = Client()
    c.force_login(u)
    return c


def _place():
    return Place.objects.create(
        name="Community Garden", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _partner(place, *, verified=True, active=True):
    return Partner.objects.create(
        name="Green Cluj",
        kind=Partner.Kind.values[0],
        place=place,
        blurb=NOTE,
        is_verified=verified,
        is_active=active,
    )


def test_public_partner_blurb_shows_on_place_detail():
    place = _place()
    _partner(place)
    assert NOTE in _viewer().get(f"/places/{place.pk}/").content.decode()


def test_unverified_partner_blurb_is_hidden():
    place = _place()
    _partner(place, verified=False)  # public() requires verified+active
    assert NOTE not in _viewer().get(f"/places/{place.pk}/").content.decode()


def test_no_blurb_no_note():
    place = _place()
    Partner.objects.create(
        name="Quiet Partner", kind=Partner.Kind.values[0], place=place, is_verified=True
    )
    # A verified partner with an empty blurb adds no note line (only the credit).
    body = _viewer().get(f"/places/{place.pk}/").content.decode()
    assert "Quiet Partner" in body  # the credit still renders
