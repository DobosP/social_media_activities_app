import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.places.models import Place
from apps.places.views import MAX_PLACES_PAGE_SIZE, CappedGeoJsonPagination, PlaceViewSet

pytestmark = pytest.mark.django_db


@pytest.fixture
def client():
    return APIClient()


def _seed_places(n):
    Place.objects.bulk_create(
        Place(
            name=f"P{i}",
            location=Point(23.59 + i * 0.0001, 46.77, srid=4326),
            source="osm",
            osm_type="node",
            osm_id=1000 + i,
        )
        for i in range(n)
    )


def test_viewset_uses_capped_pagination():
    # The cap is wired onto the viewset, not just defined in isolation.
    assert PlaceViewSet.pagination_class is CappedGeoJsonPagination
    assert CappedGeoJsonPagination.max_page_size == MAX_PLACES_PAGE_SIZE == 500


def test_page_size_is_capped_to_max(client):
    # A client asking for an absurd page size gets at most MAX_PLACES_PAGE_SIZE rows.
    _seed_places(MAX_PLACES_PAGE_SIZE + 5)
    resp = client.get("/api/places/", {"page_size": 100000})
    assert resp.status_code == 200
    body = resp.json()
    assert body["type"] == "FeatureCollection"
    assert len(body["features"]) == MAX_PLACES_PAGE_SIZE
    # There is still more data beyond this page (the cap, not the table size, limited us).
    assert body["count"] == MAX_PLACES_PAGE_SIZE + 5
    assert body["next"] is not None


def test_limit_param_does_not_bypass_cap(client):
    # GeoJsonPagination is page-number based, so ?limit is inert and cannot be used
    # to dump the table around the page-size cap; default PAGE_SIZE still applies.
    _seed_places(120)
    resp = client.get("/api/places/", {"limit": 100000})
    assert resp.status_code == 200
    body = resp.json()
    # Falls back to the configured default PAGE_SIZE (50), never the whole table.
    assert len(body["features"]) == 50
    assert body["count"] == 120


def test_place_data_is_publicly_readable(client):
    # No auth: place data is intentionally public (defense-in-depth permission decl).
    _seed_places(1)
    resp = client.get("/api/places/")
    assert resp.status_code == 200
