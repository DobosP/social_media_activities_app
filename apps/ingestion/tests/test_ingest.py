import os
from unittest import mock

import pytest
from django.core.management import call_command
from django.core.management.base import CommandError
from django.test import override_settings

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


@pytest.mark.django_db
@override_settings(
    INGESTION_EXTRA_ADAPTERS={"roedu": "apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter"}
)
def test_ingest_places_reports_a_refused_roedu_product_instead_of_zero_places():
    """`places: created=0` must not be how a refused venues product looks."""
    from apps.ingestion.sources.roedu_client import RoeduProductUnavailable

    class RefusingClient:
        def __init__(self, *args, **kwargs):
            pass

        def iter_required(self, product, *, limit=200, max_records=None, **filters):
            raise RoeduProductUnavailable(product, note="venues withheld by the policy gate")

    # ROEDU_APP_PACK would send the adapter down the app-pack branch instead; the docs
    # tell operators to export it, so a developer's shell must not decide this test.
    with mock.patch.dict(os.environ, {}, clear=False):
        os.environ.pop("ROEDU_APP_PACK", None)
        with mock.patch("apps.ingestion.sources.ro_scraper.RoeduClient", RefusingClient):
            with pytest.raises(CommandError) as caught:
                call_command("ingest_places", "--source", "roedu", "--city", "Cluj-Napoca")

    assert "venues withheld by the policy gate" in str(caught.value)
    assert Place.objects.filter(source=Place.Source.ROEDU).count() == 0


@pytest.mark.django_db
@override_settings(
    INGESTION_EXTRA_ADAPTERS={"roedu": "apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter"}
)
def test_a_mid_walk_refusal_still_reports_what_the_run_already_wrote(capsys):
    """`_upsert` commits per row, so the counts are the only record of what landed."""
    from apps.ingestion.sources.roedu_client import RoeduProductUnavailable

    venue = {
        "id": "venue-1",
        "name": "Teatrul National",
        "lat": 46.7712,
        "lon": 23.5949,
        "city": "Cluj-Napoca",
    }

    class RefusingMidWalk:
        def __init__(self, *args, **kwargs):
            pass

        def iter_required(self, product, *, limit=200, max_records=None, **filters):
            yield venue
            raise RoeduProductUnavailable(product, note="policy gate closed", records=1)

    with mock.patch.dict(os.environ, {}, clear=False):
        os.environ.pop("ROEDU_APP_PACK", None)
        with mock.patch("apps.ingestion.sources.ro_scraper.RoeduClient", RefusingMidWalk):
            with pytest.raises(CommandError):
                call_command("ingest_places", "--source", "roedu", "--city", "Cluj-Napoca")

    assert "places:" in capsys.readouterr().out
    assert Place.objects.filter(source=Place.Source.ROEDU).count() == 1
