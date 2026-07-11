from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import Cohort, User
from apps.events.models import Event
from apps.places.models import Place, PlaceActivity, PlaceCover
from apps.social.models import Activity
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
    assert feature["properties"]["image_thumb"] is None


@pytest.mark.django_db
def test_geojson_image_thumb_uses_cover_photo_only(client):
    place = _make_place("Covered Library", 23.60, 46.78, 4)
    PlaceCover.objects.create(
        place=place,
        source=PlaceCover.Source.WIKIMEDIA,
        storage_key="place-covers/covered.jpg",
        content_type="image/jpeg",
    )

    resp = client.get("/api/places/")

    assert resp.status_code == 200
    props = resp.json()["features"][0]["properties"]
    assert props["image_thumb"].startswith("/api/media/place-cover-file/")


@pytest.mark.django_db
def test_place_api_exposes_attribution_credit(client):
    place = Place.objects.create(
        name="RO-EDU Theatre",
        location=Point(23.60, 46.78, srid=4326),
        source=Place.Source.ROEDU,
        external_id="theatre-1",
        attribution="RO-EDU",
        license_name="CC BY 4.0",
        provenance_url="https://data.example/venues/theatre-1",
    )
    reading = ActivityType.objects.get(slug="reading")
    PlaceActivity.objects.create(place=place, activity=reading, confidence=0.95)

    resp = client.get("/api/places/")
    assert resp.status_code == 200
    props = resp.json()["features"][0]["properties"]
    assert props["attribution_credit"] == {
        "attribution": "RO-EDU",
        "license_name": "CC BY 4.0",
        "provenance_url": "https://data.example/venues/theatre-1",
    }


@pytest.mark.django_db
def test_category_properties_and_filter_use_top_level_non_disputed_edges(client):
    basketball = ActivityType.objects.get(slug="basketball")
    reading = ActivityType.objects.get(slug="reading")
    court = _make_place("Court", 23.59, 46.77, 20)
    library = _make_place("Library", 23.60, 46.78, 21)
    PlaceActivity.objects.create(place=court, activity=basketball, confidence=0.9)
    PlaceActivity.objects.create(place=court, activity=reading, confidence=0.9, is_disputed=True)
    PlaceActivity.objects.create(place=library, activity=reading, confidence=0.95)

    resp = client.get("/api/places/", {"category": "sport"})

    assert resp.status_code == 200
    features = resp.json()["features"]
    assert [f["properties"]["name"] for f in features] == ["Court"]
    props = features[0]["properties"]
    assert props["categories"] == ["sport"]
    assert props["category_labels"] == ["Sport"]

    resp = client.get("/api/places/", {"category": "reading"})
    assert [f["properties"]["name"] for f in resp.json()["features"]] == ["Library"]


@pytest.mark.django_db
def test_has_upcoming_property_and_filter_include_public_activities_and_events(client):
    basketball = ActivityType.objects.get(slug="basketball")
    court = _make_place("Court", 23.59, 46.77, 30)
    hall = _make_place("Hall", 23.60, 46.78, 31)
    quiet = _make_place("Quiet", 23.61, 46.79, 32)
    for place in (court, hall, quiet):
        PlaceActivity.objects.create(place=place, activity=basketball, confidence=0.9)
    owner = User.objects.create_user(username="map-owner", password="pw")
    Activity.objects.create(
        owner=owner,
        place=court,
        activity_type=basketball,
        title="Public pickup",
        starts_at=timezone.now() + timedelta(days=1),
        cohort=Cohort.ADULT,
        status=Activity.Status.OPEN,
        is_publicly_listed=True,
    )
    Event.objects.create(
        place=hall, title="Venue calendar", starts_at=timezone.now() + timedelta(days=2)
    )
    Event.objects.create(
        place=quiet,
        title="Cancelled venue calendar",
        starts_at=timezone.now() + timedelta(days=2),
        lifecycle_status=Event.LifecycleStatus.CANCELLED,
    )

    resp = client.get("/api/places/")

    assert resp.status_code == 200
    by_name = {f["properties"]["name"]: f["properties"] for f in resp.json()["features"]}
    assert by_name["Court"]["has_upcoming"] is True
    assert by_name["Hall"]["has_upcoming"] is True
    assert by_name["Quiet"]["has_upcoming"] is False

    resp = client.get("/api/places/", {"has_upcoming": "true"})
    assert {f["properties"]["name"] for f in resp.json()["features"]} == {
        "Court",
        "Hall",
    }


@pytest.mark.django_db
def test_places_map_geojson_query_count_is_bounded(client, django_assert_num_queries):
    basketball = ActivityType.objects.get(slug="basketball")
    for i in range(4):
        place = _make_place(f"Court {i}", 23.59 + i / 1000, 46.77, 40 + i)
        PlaceActivity.objects.create(place=place, activity=basketball, confidence=0.9)

    with django_assert_num_queries(4):
        resp = client.get("/api/places/", {"page_size": 500})

    assert resp.status_code == 200
    assert len(resp.json()["features"]) == 4
