"""W2-F14 (web): the series owner stages a one-shot heads-up; a non-owner can't."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import ActivitySeries
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _series(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="f14-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="f14-run", defaults={"name": "Running", "category": cat}
    )
    place = Place.objects.create(
        name="Track", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_series(
        owner,
        place=place,
        activity_type=atype,
        title="Weekly run",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=timezone.now() + timedelta(days=2),
    )


def test_owner_can_stage_a_next_note():
    owner = _user("f14owner")
    series = _series(owner)
    c = Client()
    c.force_login(owner)
    page = c.get(f"/activities/series/{series.id}/").content.decode()
    assert "Heads-up for the next meetup" in page
    resp = c.post(
        f"/activities/series/{series.id}/next-note/",
        {"next_instance_note": "Bring cleats this week."},
    )
    assert resp.status_code == 302
    series.refresh_from_db()
    assert series.next_instance_note == "Bring cleats this week."


def test_non_owner_cannot_stage_a_next_note():
    owner = _user("f14owner2")
    intruder = _user("f14intruder")
    series = _series(owner)
    c = Client()
    c.force_login(intruder)
    # A non-owner can't even see the series (visible_series is owner-scoped) -> 404.
    resp = c.post(f"/activities/series/{series.id}/next-note/", {"next_instance_note": "sneaky"})
    assert resp.status_code == 404
    series.refresh_from_db()
    assert series.next_instance_note == ""
