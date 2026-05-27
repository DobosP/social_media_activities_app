import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.places.enrichment.opening_hours import parse_opening_hours
from apps.places.models import Place


@pytest.mark.django_db
def test_open_now_and_hours_in_response():
    Place.objects.create(
        name="Library",
        location=Point(23.60, 46.78, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=1,
        opening_hours_raw="24/7",
        opening_hours=parse_opening_hours("24/7"),
    )
    resp = APIClient().get("/api/places/")
    assert resp.status_code == 200
    props = resp.json()["features"][0]["properties"]
    assert props["open_now"] is True
    assert props["opening_hours"]["mo"] == [[0, 1440]]


@pytest.mark.django_db
def test_open_now_null_when_unknown():
    Place.objects.create(
        name="Mystery",
        location=Point(23.60, 46.78, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=2,
    )
    resp = APIClient().get("/api/places/")
    props = resp.json()["features"][0]["properties"]
    assert props["open_now"] is None
