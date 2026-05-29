import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.discovery.views import MAX_RESULTS
from apps.places.models import Place

pytestmark = pytest.mark.django_db


def _seed_many_places(n):
    Place.objects.bulk_create(
        Place(
            name=f"DiscPlace{i}",
            location=Point(23.6 + i * 0.0001, 46.77, srid=4326),
            source=Place.Source.OSM,
            address_city="Cluj-Napoca",
        )
        for i in range(n)
    )


def test_near_me_response_is_capped_to_max_results():
    # PERF-3: the bare APIView slices results with a hard, server-side cap.
    _seed_many_places(MAX_RESULTS + 10)
    resp = APIClient().get("/api/discovery/near-me/")
    assert resp.status_code == 200
    assert len(resp.data) == MAX_RESULTS
