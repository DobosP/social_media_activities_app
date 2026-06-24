"""F9 — public meetup-place gate: the read-time venue-class resolver.

Relies on the seeded ChildVenueClass allowlist (places/migrations 0007) for library/park/etc.
"""

import pytest
from django.contrib.gis.geos import Point

from apps.places.models import ApprovedChildVenue, ChildVenueClass, Place
from apps.places.services import is_child_safe_venue, public_child_venue_class

pytestmark = pytest.mark.django_db

PT = Point(23.6, 46.77, srid=4326)


def _place(source=Place.Source.OSM, raw_tags=None, **kw):
    return Place.objects.create(
        name=kw.pop("name", "V"), location=PT, source=source, raw_tags=raw_tags or {}, **kw
    )


def test_osm_library_is_allowed():
    p = _place(raw_tags={"amenity": "library"})
    assert public_child_venue_class(p) == "allowed"
    assert is_child_safe_venue(p) is True


def test_osm_park_is_allowed():
    assert is_child_safe_venue(_place(raw_tags={"leisure": "park"})) is True


def test_osm_bar_is_unknown():
    p = _place(raw_tags={"amenity": "bar"})
    assert public_child_venue_class(p) == "unknown"
    assert is_child_safe_venue(p) is False


def test_osm_untagged_is_unknown():
    assert is_child_safe_venue(_place(raw_tags={})) is False


def test_user_place_is_unknown():
    assert is_child_safe_venue(_place(source=Place.Source.USER, raw_tags={})) is False


def test_overture_category_match_is_allowed():
    p = _place(source=Place.Source.OVERTURE, raw_tags={"overture:category": "library"})
    assert is_child_safe_venue(p) is True


def test_overture_alternate_category_match_is_allowed():
    p = _place(
        source=Place.Source.OVERTURE,
        raw_tags={"overture:category": "cafe", "overture:alternate": ["park"]},
    )
    assert is_child_safe_venue(p) is True


def test_overture_unmatched_category_is_unknown():
    p = _place(source=Place.Source.OVERTURE, raw_tags={"overture:category": "nightclub"})
    assert is_child_safe_venue(p) is False


def test_google_source_is_unknown_failclosed():
    # Google tag shape isn't resolved yet -> fail-closed to unknown (never silently allowed).
    assert is_child_safe_venue(_place(source=Place.Source.GOOGLE, raw_tags={"x": "y"})) is False


def test_roedu_cultural_tags_are_unknown_failclosed():
    p = _place(source=Place.Source.ROEDU, raw_tags={"amenity": "theatre"})
    assert public_child_venue_class(p) == "unknown"
    assert is_child_safe_venue(p) is False


def test_roedu_staff_approval_is_allowed():
    p = _place(source=Place.Source.ROEDU, raw_tags={"tourism": "museum"})
    ApprovedChildVenue.objects.create(place=p)
    assert public_child_venue_class(p) == "allowed"
    assert is_child_safe_venue(p) is True


def test_staff_approval_overrides_unknown():
    p = _place(raw_tags={"amenity": "bar"})  # would be unknown
    assert is_child_safe_venue(p) is False
    ApprovedChildVenue.objects.create(place=p)
    assert public_child_venue_class(p) == "allowed"
    assert is_child_safe_venue(p) is True


def test_inactive_class_does_not_match():
    ChildVenueClass.objects.filter(key="library").update(is_active=False)
    assert is_child_safe_venue(_place(raw_tags={"amenity": "library"})) is False


def test_empty_osm_match_never_blanket_allows():
    # A misconfigured class with empty criteria must NOT match every place.
    ChildVenueClass.objects.create(key="bad", label="Bad", osm_match={}, overture_categories=[])
    assert is_child_safe_venue(_place(raw_tags={"amenity": "bar"})) is False


def test_none_place_is_unknown():
    assert public_child_venue_class(None) == "unknown"
