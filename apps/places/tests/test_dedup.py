import pytest
from django.contrib.gis.geos import Point

from apps.places.enrichment.dedup import (
    find_duplicate,
    merge_places,
    name_similarity,
    normalize_name,
)
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityType


def _place(name, lon, lat, *, source="osm", osm_id=None, external_id=""):
    return Place.objects.create(
        name=name,
        location=Point(lon, lat, srid=4326),
        source=source,
        osm_type="node" if source == "osm" else "",
        osm_id=osm_id,
        external_id=external_id,
    )


def test_normalize_name_strips_accents_and_punctuation():
    assert normalize_name("Café Central!") == "cafe central"
    assert normalize_name("  Multiple   Spaces ") == "multiple spaces"


def test_name_similarity():
    assert name_similarity("Central Library", "central library.") == 1.0
    assert name_similarity("Central Library", "City Pool") < 0.5


@pytest.mark.django_db
def test_find_duplicate_matches_close_and_similar():
    _place("Central Library", 23.5900, 46.7700, source="osm", osm_id=1)
    point = Point(23.59005, 46.77002, srid=4326)  # ~5m away
    match = find_duplicate(point, "Central Library", exclude_source="overture")
    assert match is not None and match.osm_id == 1


@pytest.mark.django_db
def test_find_duplicate_rejects_far_or_dissimilar():
    _place("Central Library", 23.5900, 46.7700, source="osm", osm_id=1)
    # Same name but far away (~1.5km).
    far = Point(23.61, 46.77, srid=4326)
    assert find_duplicate(far, "Central Library") is None
    # Close but different name.
    close = Point(23.59005, 46.77002, srid=4326)
    assert find_duplicate(close, "Pizza Place") is None


@pytest.mark.django_db
def test_merge_places_folds_edges_and_records_provenance():
    osm = _place("Central Library", 23.59, 46.77, source="osm", osm_id=1)
    overture = _place("Central Library", 23.59, 46.77, source="overture", external_id="ov-9")
    reading = ActivityType.objects.get(slug="reading")
    board = ActivityType.objects.get(slug="board_games")
    PlaceActivity.objects.create(place=osm, activity=reading, confidence=0.95, source="osm")
    PlaceActivity.objects.create(place=overture, activity=board, confidence=0.8, source="overture")

    merge_places(osm, overture)

    assert not Place.objects.filter(pk=overture.pk).exists()
    osm.refresh_from_db()
    slugs = {e.activity.slug for e in osm.place_activities.all()}
    assert slugs == {"reading", "board_games"}
    assert osm.raw_tags["merged_sources"] == [{"source": "overture", "external_id": "ov-9"}]


@pytest.mark.django_db
def test_merge_places_keeps_protected_edge():
    osm = _place("Court", 23.59, 46.77, source="osm", osm_id=1)
    overture = _place("Court", 23.59, 46.77, source="overture", external_id="ov-1")
    basketball = ActivityType.objects.get(slug="basketball")
    confirmed = PlaceActivity.objects.create(
        place=osm,
        activity=basketball,
        confidence=1.0,
        origin=PlaceActivity.Origin.CONFIRMED,
        source="osm",
    )
    PlaceActivity.objects.create(
        place=overture, activity=basketball, confidence=0.5, source="overture"
    )

    merge_places(osm, overture)
    confirmed.refresh_from_db()
    assert confirmed.origin == PlaceActivity.Origin.CONFIRMED
    assert confirmed.confidence == 1.0
