"""W4-F14: an optional city-Area filter on the /events/ list, narrowing the already-F25-gated
upcoming events to one part of the city (address_city only — no coordinate stored)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.communities.models import Area
from apps.events.models import Event
from apps.places.models import Place
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _client():
    u = User.objects.create_user(username="f14v", password="pw", display_name="f14v")
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    c = Client()
    c.force_login(u)
    return c


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="w4f14-sport", defaults={"name": "Sport"})
    return ActivityType.objects.get_or_create(
        slug="w4f14-bball", defaults={"name": "Basketball", "category": cat}
    )[0]


def _place(name, city, lon):
    return Place.objects.create(
        name=name, location=Point(lon, 46.77, srid=4326), source=Place.Source.OSM, address_city=city
    )


def _event(title, place):
    return Event.objects.create(
        title=title,
        starts_at=timezone.now() + timedelta(days=3),
        place=place,
        activity_type=_type(),
    )


@pytest.fixture
def two_cities():
    Area.objects.create(
        city="Cluj-Napoca", slug="cluj", name="Cluj-Napoca", derive_method=Area.DeriveMethod.CITY
    )
    _event("Cluj reading night", _place("Cluj Hall", "Cluj-Napoca", 23.6))
    _event("Bucharest run", _place("Bucharest Hall", "Bucharest", 26.1))


def test_no_area_shows_all_events(two_cities):
    body = _client().get("/events/").content.decode()
    assert "Cluj reading night" in body
    assert "Bucharest run" in body


def test_area_filter_narrows_to_the_city(two_cities):
    body = _client().get("/events/?area=cluj").content.decode()
    assert "Cluj reading night" in body
    assert "Bucharest run" not in body  # other-city event dropped
    assert "Showing events in" in body  # the honesty banner


def test_unknown_area_slug_is_graceful(two_cities):
    # A bad ?area= resolves to no Area -> no filter (never a 500, never an empty silent drop).
    body = _client().get("/events/?area=nope").content.decode()
    assert "Cluj reading night" in body
    assert "Bucharest run" in body
