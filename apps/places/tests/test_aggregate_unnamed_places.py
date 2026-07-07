from datetime import timedelta
from io import StringIO

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import Cohort, User
from apps.places.models import Place, PlaceActivity
from apps.places.services import derived_place_label
from apps.social.models import Activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _type(slug="lane-football", name="fotbal"):
    sport, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    activity, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": name, "category": sport}
    )
    return activity


def _place(name, lon, lat, tags, osm_id):
    return Place.objects.create(
        name=name,
        location=Point(lon, lat, srid=4326),
        source=Place.Source.OSM,
        osm_type="way",
        osm_id=osm_id,
        raw_tags=tags,
        address_city="Cluj-Napoca",
    )


def _complex(osm_id=1, lon=23.6, lat=46.77):
    return _place(
        "Baza Sportiva",
        lon,
        lat,
        {"leisure": "sports_centre", "name": "Baza Sportiva"},
        osm_id,
    )


def _field(osm_id=2, lon=23.6004, lat=46.7701, *, name=""):
    return _place(name, lon, lat, {"leisure": "pitch", "sport": "soccer"}, osm_id)


def test_aggregate_merges_edges_keeps_higher_confidence_and_deletes_child():
    football = _type()
    parent = _complex()
    child = _field()
    PlaceActivity.objects.create(place=parent, activity=football, confidence=0.4)
    PlaceActivity.objects.create(
        place=child, activity=football, confidence=0.9, mapping_rule="football_pitch"
    )

    call_command("aggregate_unnamed_places")

    assert not Place.objects.filter(pk=child.pk).exists()
    edge = parent.place_activities.get(activity=football)
    assert edge.confidence == 0.9
    assert edge.origin == PlaceActivity.Origin.INFERRED
    assert edge.mapping_rule == "football_pitch"


def test_aggregate_leaves_named_far_and_dependent_places_untouched():
    football = _type()
    parent = _complex(osm_id=10)
    named = _field(osm_id=11, name="Teren 1")
    far = _field(osm_id=12, lon=23.63, lat=46.79)
    dependent = _field(osm_id=13, lon=23.6002, lat=46.7702)
    for place in (named, far, dependent):
        PlaceActivity.objects.create(place=place, activity=football, confidence=0.9)
    owner = User.objects.create_user(username="lane-owner", password="pw")
    Activity.objects.create(
        owner=owner,
        place=dependent,
        activity_type=football,
        title="Scheduled match",
        starts_at=timezone.now() + timedelta(days=1),
        cohort=Cohort.ADULT,
    )

    call_command("aggregate_unnamed_places")

    assert Place.objects.filter(pk=named.pk).exists()
    assert Place.objects.filter(pk=far.pk).exists()
    assert Place.objects.filter(pk=dependent.pk).exists()
    assert parent.place_activities.count() == 0


def test_aggregate_dry_run_reports_without_changing():
    football = _type()
    parent = _complex(osm_id=20)
    child = _field(osm_id=21)
    PlaceActivity.objects.create(place=child, activity=football, confidence=0.9)
    out = StringIO()

    call_command("aggregate_unnamed_places", "--dry-run", stdout=out)

    assert "DRY RUN" in out.getvalue()
    assert "would_merge=1" in out.getvalue()
    assert Place.objects.filter(pk=child.pk).exists()
    assert parent.place_activities.count() == 0


def test_derived_label_uses_top_non_disputed_activity_and_named_place_is_unchanged():
    football = _type(name="fotbal")
    blank = _field(osm_id=30)
    named = _field(osm_id=31, name="Terenul Mare")
    PlaceActivity.objects.create(place=blank, activity=football, confidence=0.9)
    PlaceActivity.objects.create(place=named, activity=football, confidence=0.9)

    assert derived_place_label(blank) == "Teren fotbal"
    assert derived_place_label(named) == "Terenul Mare"


def test_serializer_emits_derived_name_only_when_blank():
    football = _type(name="fotbal")
    blank = _field(osm_id=40)
    named = _field(osm_id=41, name="Terenul Mare")
    PlaceActivity.objects.create(place=blank, activity=football, confidence=0.9)
    PlaceActivity.objects.create(place=named, activity=football, confidence=0.9)

    resp = APIClient().get("/api/places/", {"page_size": 500})

    assert resp.status_code == 200
    names = {feature["properties"]["name"] for feature in resp.json()["features"]}
    assert "Teren fotbal" in names
    assert "Terenul Mare" in names
