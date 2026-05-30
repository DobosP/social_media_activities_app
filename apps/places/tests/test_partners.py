"""F37: verified civic partners — text-only, manager-gated public visibility, sanitised links."""

import pytest
from django.contrib.gis.geos import Point
from django.core.exceptions import ValidationError

from apps.places.models import Partner, Place
from apps.places.services import partner_for_place, verified_partners

pytestmark = pytest.mark.django_db


def _place(name="Venue"):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _partner(name="Lib", *, verified=True, active=True, place=None, website="", blurb=""):
    return Partner.objects.create(
        name=name,
        kind=Partner.Kind.LIBRARY,
        is_verified=verified,
        is_active=active,
        place=place,
        website=website,
        blurb=blurb,
    )


def test_website_sanitised_on_save():
    assert _partner(website="javascript:alert(1)").website == ""
    assert _partner(name="Ok", website="https://example.org").website == "https://example.org"


def test_public_excludes_unverified_and_inactive():
    _partner(name="Good", verified=True, active=True)
    _partner(name="Unverified", verified=False, active=True)
    _partner(name="Inactive", verified=True, active=False)
    assert {p.name for p in verified_partners()} == {"Good"}


def test_partner_for_place_only_when_verified():
    place = _place("A")
    _partner(name="Steward", verified=True, place=place)
    assert partner_for_place(place).name == "Steward"
    place2 = _place("B")
    _partner(name="Unver", verified=False, place=place2)
    assert partner_for_place(place2) is None  # unverified never surfaced


def test_blurb_over_cap_rejected():
    p = Partner(name="Long", kind=Partner.Kind.NGO, blurb="x" * 281)
    with pytest.raises(ValidationError):
        p.full_clean()


def test_partner_has_no_image_or_file_field():
    # Text-only by construction — a logo/banner field would create an ad surface.
    types = {f.get_internal_type() for f in Partner._meta.get_fields()}
    assert "ImageField" not in types
    assert "FileField" not in types
