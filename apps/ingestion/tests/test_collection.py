"""Tests for location collection: parks/public-place selectors, website/phone
capture, the --with-website filter, and seeding booking deep-links from websites."""

import pytest

from apps.ingestion.mapping import match_element
from apps.ingestion.sources.overpass import SELECTORS, OverpassAdapter


def test_park_and_public_place_selectors_present():
    joined = "".join(SELECTORS)
    for needle in ('"park"', '"library"', '"fitness_centre"', '"arts_centre"'):
        assert needle in joined


def test_overpass_captures_website_and_phone():
    element = {
        "type": "node",
        "id": 1,
        "lat": 46.77,
        "lon": 23.6,
        "tags": {
            "name": "City Sports Hall",
            "leisure": "sports_hall",
            "website": "https://example.ro/book",
            "phone": "+40 264 000000",
        },
    }
    raw = OverpassAdapter.element_to_raw_place(element)
    assert raw.website == "https://example.ro/book"
    assert raw.phone == "+40 264 000000"


def test_overpass_website_falls_back_to_contact_tag():
    element = {
        "type": "node",
        "id": 2,
        "lat": 46.0,
        "lon": 23.0,
        "tags": {"name": "Park Cafe", "contact:website": "https://cafe.example.ro"},
    }
    raw = OverpassAdapter.element_to_raw_place(element)
    assert raw.website == "https://cafe.example.ro"


def test_park_maps_to_outdoor_activities():
    slugs = {slug for slug, _, _ in match_element({"leisure": "park", "name": "Central Park"})}
    assert {"football", "basketball", "streetball", "chess"} <= slugs


# --- DB-backed: ingest upsert + booking seeding ---

pytestmark_db = pytest.mark.django_db


@pytest.mark.django_db
def test_ingest_stores_website_and_with_website_filter(monkeypatch):
    from apps.ingestion.sources.base import RawPlace
    from apps.places.models import Place
    from apps.taxonomy.models import ActivityCategory, ActivityType

    cat = ActivityCategory.objects.create(slug="c-coll", name="Sport")
    ActivityType.objects.get_or_create(
        slug="basketball", defaults={"name": "Basketball", "category": cat}
    )

    raws = [
        RawPlace(
            source="osm",
            osm_type="node",
            osm_id=101,
            name="Bookable Hall",
            lon=23.6,
            lat=46.77,
            tags={"leisure": "sports_hall"},
            website="https://venue.example.ro",
            attribution="OpenStreetMap contributors",
            license_name="ODbL",
            provenance_url="https://www.openstreetmap.org/node/101",
        ),
        RawPlace(
            source="osm",
            osm_type="node",
            osm_id=102,
            name="No Site Park",
            lon=23.61,
            lat=46.78,
            tags={"leisure": "park"},
        ),
    ]

    from apps.ingestion.management.commands import ingest_places as cmd_mod

    monkeypatch.setattr(
        cmd_mod.OverpassAdapter, "fetch", lambda self, **kw: iter(raws), raising=True
    )

    from django.core.management import call_command

    call_command("ingest_places", "--source", "osm", "--city", "Cluj", "--with-website")

    assert Place.objects.filter(osm_id=101).exists()
    assert not Place.objects.filter(osm_id=102).exists()  # filtered out (no website)
    place = Place.objects.get(osm_id=101)
    assert place.website == "https://venue.example.ro"
    assert place.attribution == "OpenStreetMap contributors"
    assert place.license_name == "ODbL"
    assert place.provenance_url == "https://www.openstreetmap.org/node/101"


@pytest.mark.django_db
def test_seed_booking_links_from_websites():
    from django.contrib.gis.geos import Point
    from django.core.management import call_command

    from apps.booking.models import PlaceBookingInfo
    from apps.places.models import Place

    bookable = Place.objects.create(
        name="Venue",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        website="https://venue.example.ro",
    )
    Place.objects.create(
        name="No site",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
    )

    call_command("seed_booking_links")

    info = PlaceBookingInfo.objects.get(place=bookable)
    assert info.deep_link == "https://venue.example.ro"
    assert PlaceBookingInfo.objects.count() == 1  # only the one with a website
