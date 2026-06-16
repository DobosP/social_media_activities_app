"""W4-F12: convene-around-this-event — seed an F27 interest gauge from a real public event, so a
browser who finds an event but no meetup can float low-commitment demand in one tap."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event
from apps.places.models import Place
from apps.social.models import UserPlaceProposal
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="w4f12-sport", defaults={"name": "Sport"})
    return ActivityType.objects.get_or_create(
        slug="w4f12-bball", defaults={"name": "Basketball", "category": cat}
    )[0]


def _place(name="Court", source=Place.Source.OSM):
    return Place.objects.create(name=name, location=Point(23.6, 46.77, srid=4326), source=source)


def _event(place, activity_type):
    return Event.objects.create(
        title="Open basketball night",
        starts_at=timezone.now() + timedelta(days=3),
        place=place,
        activity_type=activity_type,
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def test_convene_seeds_gauge_form_from_event():
    user = _user("f12a")
    at = _type()
    place = _place()
    event = _event(place, at)
    resp = _client(user).get(f"/gauges/new/?event={event.id}")
    assert resp.status_code == 200
    initial = resp.context["form"].initial
    assert initial.get("place") == place.id
    assert initial.get("activity_type") == at.id
    # The availability window is deliberately NOT seeded — the user picks when THEY are free.
    assert not initial.get("coarse_window")


def test_convene_link_shows_on_public_event_detail():
    user = _user("f12b")
    event = _event(_place(), _type())
    body = _client(user).get(f"/events/{event.id}/").content.decode()
    assert "gauge interest" in body
    assert f"/gauges/new/?event={event.id}" in body


def test_convene_does_not_seed_a_pending_place():
    # A gauge can never be seeded at a still-pending user-proposed venue (events_with_public_places
    # excludes it), so a crafted ?event= pointing at a pending-place event injects no place.
    user = _user("f12c")
    proposer = _user("f12c_prop")
    pending = _place("Pending Gym", source=Place.Source.USER)
    UserPlaceProposal.objects.create(
        place=pending, proposer=proposer, status=UserPlaceProposal.Status.PENDING
    )
    event = _event(pending, _type())
    resp = _client(user).get(f"/gauges/new/?event={event.id}")
    assert resp.status_code == 200
    assert resp.context["form"].initial.get("place") is None
