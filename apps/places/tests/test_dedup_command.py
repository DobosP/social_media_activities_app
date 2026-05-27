import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command

from apps.places.models import Place


def _dup_pair():
    osm = Place.objects.create(
        name="City Sports Hall",
        location=Point(23.59, 46.77, srid=4326),
        source="osm",
        osm_type="node",
        osm_id=1,
    )
    overture = Place.objects.create(
        name="City Sports Hall",
        location=Point(23.59001, 46.77001, srid=4326),
        source="overture",
        external_id="ov-1",
    )
    return osm, overture


@pytest.mark.django_db
def test_dedup_dry_run_changes_nothing():
    _dup_pair()
    call_command("dedup_places")
    assert Place.objects.count() == 2


@pytest.mark.django_db
def test_dedup_apply_merges_into_osm_canonical():
    osm, overture = _dup_pair()
    call_command("dedup_places", "--apply")
    assert Place.objects.count() == 1
    survivor = Place.objects.get()
    assert survivor.pk == osm.pk  # osm preferred over overture as canonical
    assert survivor.raw_tags["merged_sources"][0]["external_id"] == "ov-1"
