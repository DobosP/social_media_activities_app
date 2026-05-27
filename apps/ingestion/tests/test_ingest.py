import pytest
from django.core.management import call_command

from apps.ingestion.sources.overpass import OverpassAdapter
from apps.places.models import Place, PlaceActivity

# Recorded Overpass response (no network). Covers: a mapped node, a mapped way
# (centroid), an unmapped node, and a way missing its centroid (skipped).
SAMPLE = {
    "elements": [
        {
            "type": "node",
            "id": 1,
            "lat": 46.77,
            "lon": 23.59,
            "tags": {
                "amenity": "library",
                "name": "Central Library",
                "addr:city": "Cluj-Napoca",
            },
        },
        {
            "type": "way",
            "id": 2,
            "center": {"lat": 46.78, "lon": 23.60},
            "tags": {"leisure": "pitch", "sport": "basketball", "name": "Court"},
        },
        {"type": "node", "id": 3, "lat": 46.76, "lon": 23.58, "tags": {"amenity": "bank"}},
        {"type": "way", "id": 4, "tags": {"leisure": "pitch", "sport": "tennis"}},
    ]
}


@pytest.fixture
def patched_overpass(monkeypatch):
    monkeypatch.setattr(OverpassAdapter, "_post", lambda self, query: SAMPLE)


def _ingest():
    call_command("ingest_places", "--source", "osm", "--city", "Cluj-Napoca")


@pytest.mark.django_db
def test_ingest_creates_places_and_edges(patched_overpass):
    _ingest()
    # node 1 + way 2 + node 3 = 3 places; way 4 skipped (no centroid).
    assert Place.objects.count() == 3

    library = Place.objects.get(osm_type="node", osm_id=1)
    assert library.address_city == "Cluj-Napoca"
    assert library.place_activities.filter(activity__slug="reading").exists()

    court = Place.objects.get(osm_type="way", osm_id=2)
    edge = court.place_activities.get(activity__slug="basketball")
    assert edge.confidence == 0.9
    assert edge.mapping_rule == "bball_pitch"

    bank = Place.objects.get(osm_type="node", osm_id=3)
    assert bank.place_activities.count() == 0  # collected, but no activities


@pytest.mark.django_db
def test_ingest_is_idempotent(patched_overpass):
    _ingest()
    places, edges = Place.objects.count(), PlaceActivity.objects.count()
    _ingest()
    assert Place.objects.count() == places
    assert PlaceActivity.objects.count() == edges


@pytest.mark.django_db
def test_reingest_preserves_confirmed_edges(patched_overpass):
    _ingest()
    edge = Place.objects.get(osm_type="way", osm_id=2).place_activities.get(
        activity__slug="basketball"
    )
    edge.origin = PlaceActivity.Origin.CONFIRMED
    edge.confidence = 1.0
    edge.save()

    _ingest()
    edge.refresh_from_db()
    assert edge.origin == PlaceActivity.Origin.CONFIRMED
    assert edge.confidence == 1.0  # not clobbered back to the inferred 0.9
