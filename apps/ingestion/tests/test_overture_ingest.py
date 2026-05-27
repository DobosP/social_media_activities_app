import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command

from apps.ingestion.sources.overture import OvertureAdapter
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityType

OVERTURE_ROWS = [
    {
        "id": "ov-lib",
        "name": "Central Library",
        "category": "library",
        "alternate": [],
        "lon": 23.5900,
        "lat": 46.7700,
        "addresses": [{"locality": "Cluj-Napoca"}],
        "websites": [],
    },
    {
        "id": "ov-cafe",
        "name": "Joc Board Game Cafe",
        "category": "board_game_store",
        "alternate": [],
        "lon": 23.7000,
        "lat": 46.8000,
        "addresses": [{"locality": "Cluj-Napoca"}],
        "websites": ["https://joc.example.ro"],
    },
]


@pytest.fixture
def patched_overture(monkeypatch):
    monkeypatch.setattr(OvertureAdapter, "_query_rows", lambda self, bbox: iter(OVERTURE_ROWS))


def _ingest(**extra):
    call_command(
        "ingest_places",
        "--source",
        "overture",
        "--bbox",
        "23,46,24,47",
        "--overture-path",
        "/tmp/places.parquet",
        **extra,
    )


@pytest.mark.django_db
def test_overture_dedups_into_existing_osm_place(patched_overture):
    # Pre-existing OSM place that is the same venue as the first Overture row.
    osm_lib = Place.objects.create(
        name="Central Library",
        location=Point(23.59001, 46.77001, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=42,
    )
    reading = ActivityType.objects.get(slug="reading")
    PlaceActivity.objects.create(place=osm_lib, activity=reading, confidence=0.95, source="osm")

    _ingest()

    # The Overture library folded into the OSM place; only the cafe is new.
    assert not Place.objects.filter(source="overture", external_id="ov-lib").exists()
    osm_lib.refresh_from_db()
    assert osm_lib.raw_tags["merged_sources"] == [{"source": "overture", "external_id": "ov-lib"}]
    cafe = Place.objects.get(source="overture", external_id="ov-cafe")
    assert cafe.place_activities.filter(activity__slug="board_games").exists()
    assert cafe.raw_tags["overture:website"] == "https://joc.example.ro"


@pytest.mark.django_db
def test_overture_no_dedup_creates_separate_place(patched_overture):
    Place.objects.create(
        name="Central Library",
        location=Point(23.59001, 46.77001, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=42,
    )
    _ingest(dedup=False)
    assert Place.objects.filter(source="overture", external_id="ov-lib").exists()
    assert Place.objects.count() == 3


@pytest.mark.django_db
def test_overture_ingest_is_idempotent(patched_overture):
    _ingest()
    places, edges = Place.objects.count(), PlaceActivity.objects.count()
    _ingest()
    assert Place.objects.count() == places
    assert PlaceActivity.objects.count() == edges


@pytest.mark.django_db
def test_overture_parses_opening_hours_on_ingest(monkeypatch):
    rows = [
        {
            "id": "ov-1",
            "name": "Night Arcade",
            "category": "arcade",
            "alternate": [],
            "lon": 23.5,
            "lat": 46.7,
            "addresses": [],
            "websites": [],
        }
    ]
    monkeypatch.setattr(OvertureAdapter, "_query_rows", lambda self, bbox: iter(rows))
    # Overture rows carry no hours; set raw afterwards and enrich to verify parsing.
    _ingest()
    place = Place.objects.get(external_id="ov-1")
    place.opening_hours_raw = "Mo-Su 10:00-22:00"
    place.save(update_fields=["opening_hours_raw"])
    call_command("enrich_places", "--source", "overture")
    place.refresh_from_db()
    assert place.opening_hours["mo"] == [[600, 1320]]
