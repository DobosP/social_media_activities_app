import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityType


@pytest.fixture
def client():
    return APIClient()


def _make_place(name, lon, lat, osm_id):
    return Place.objects.create(
        name=name,
        location=Point(lon, lat, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=osm_id,
    )


@pytest.mark.django_db
def test_filter_by_activity(client):
    basketball = ActivityType.objects.get(slug="basketball")
    reading = ActivityType.objects.get(slug="reading")
    court = _make_place("Court", 23.59, 46.77, 1)
    library = _make_place("Library", 23.60, 46.78, 2)
    PlaceActivity.objects.create(place=court, activity=basketball, confidence=0.9)
    PlaceActivity.objects.create(place=library, activity=reading, confidence=0.95)

    resp = client.get("/api/places/", {"activity": "basketball"})
    assert resp.status_code == 200
    features = resp.json()["features"]
    assert len(features) == 1
    props = features[0]["properties"]
    assert props["name"] == "Court"
    assert "basketball" in [a["slug"] for a in props["activities"]]


@pytest.mark.django_db
def test_proximity_orders_nearest_first(client):
    reading = ActivityType.objects.get(slug="reading")
    near = _make_place("Near", 23.5900, 46.7700, 10)
    far = _make_place("Far", 23.7000, 46.8500, 11)
    for place in (near, far):
        PlaceActivity.objects.create(place=place, activity=reading, confidence=0.9)

    resp = client.get(
        "/api/places/",
        {"activity": "reading", "near_lon": "23.5899", "near_lat": "46.7712"},
    )
    assert resp.status_code == 200
    features = resp.json()["features"]
    names = [f["properties"]["name"] for f in features]
    assert names == ["Near", "Far"]
    distances = [f["properties"]["distance_m"] for f in features]
    assert distances[0] is not None
    assert distances[0] < distances[1]


@pytest.mark.django_db
def test_geojson_shape(client):
    reading = ActivityType.objects.get(slug="reading")
    library = _make_place("Library", 23.60, 46.78, 3)
    PlaceActivity.objects.create(place=library, activity=reading, confidence=0.95)

    resp = client.get("/api/places/")
    assert resp.status_code == 200
    body = resp.json()
    assert body["type"] == "FeatureCollection"
    feature = body["features"][0]
    assert feature["geometry"]["type"] == "Point"
    assert len(feature["geometry"]["coordinates"]) == 2
