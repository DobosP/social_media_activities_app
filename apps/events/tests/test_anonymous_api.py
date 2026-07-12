from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event
from apps.events.serializers import EventSerializer
from apps.places.models import Place
from apps.social.models import UserPlaceProposal

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _osm_place(name="City Library"):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _upcoming(**kwargs):
    kwargs.setdefault("title", "Upcoming happening")
    kwargs.setdefault("starts_at", timezone.now() + timedelta(days=2))
    kwargs.setdefault("source", Event.Source.MANUAL)
    return Event.objects.create(**kwargs)


def test_anonymous_list_returns_upcoming_events():
    _upcoming(title="Chess club night", place=_osm_place())

    resp = APIClient().get("/api/v1/events/")

    assert resp.status_code == 200
    titles = [e["title"] for e in resp.json()["results"]]
    assert "Chess club night" in titles


def test_anonymous_detail_returns_200():
    event = _upcoming(title="Reading circle", place=_osm_place())

    resp = APIClient().get(f"/api/v1/events/{event.id}/")

    assert resp.status_code == 200
    assert resp.json()["title"] == "Reading circle"


def test_anonymous_list_hides_event_at_unpublished_user_place():
    # A USER-source place stays hidden until its co-creation proposal is PUBLISHED, so an
    # event pinned to a still-PENDING proposal must not leak that place through the API.
    pending_place = Place.objects.create(
        name="Pending backyard court",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.USER,
    )
    UserPlaceProposal.objects.create(
        place=pending_place,
        proposer=_user("proposer"),
        status=UserPlaceProposal.Status.PENDING,
    )
    _upcoming(title="At pending place", place=pending_place)
    _upcoming(title="At public place", place=_osm_place())

    titles = [e["title"] for e in APIClient().get("/api/v1/events/").json()["results"]]
    assert "At public place" in titles
    assert "At pending place" not in titles


def test_anonymous_list_hides_cancelled_event():
    _upcoming(
        title="Cancelled upstream",
        source=Event.Source.SCRAPER,
        external_id="roedu:anon-cancelled",
        lifecycle_status=Event.LifecycleStatus.CANCELLED,
    )

    assert APIClient().get("/api/v1/events/").json()["count"] == 0


def test_anonymous_include_past_hides_tombstoned_event():
    # include_past widens to the full history, but a tombstoned (retracted) event is gated
    # out of every read surface — even the historical one.
    Event.objects.create(
        title="Retracted upstream",
        starts_at=timezone.now() - timedelta(days=1),
        source=Event.Source.SCRAPER,
        external_id="roedu:anon-tombstone",
        is_tombstone=True,
        lifecycle_status=Event.LifecycleStatus.REMOVED,
    )
    _upcoming(title="Still live", place=_osm_place())

    resp = APIClient().get("/api/v1/events/?include_past=true")
    assert resp.status_code == 200
    titles = [e["title"] for e in resp.json()["results"]]
    assert "Still live" in titles
    assert "Retracted upstream" not in titles


def test_anonymous_include_past_returns_past_events():
    _upcoming(title="Upcoming", place=_osm_place())
    Event.objects.create(
        title="Old one",
        starts_at=timezone.now() - timedelta(days=1),
        source=Event.Source.MANUAL,
    )

    default = APIClient().get("/api/v1/events/").json()
    history = APIClient().get("/api/v1/events/?include_past=true").json()

    assert "Old one" not in [e["title"] for e in default["results"]]
    assert "Old one" in [e["title"] for e in history["results"]]


def test_anonymous_response_exposes_no_user_or_pii_fields():
    _upcoming(title="Public event", place=_osm_place())

    item = APIClient().get("/api/v1/events/").json()["results"][0]

    # The serialized keys must be a subset of EventSerializer's declared fields — no user,
    # reporter, proposer, or other PII/relational field can leak through the anon surface.
    allowed = set(EventSerializer().fields.keys())
    assert set(item.keys()) <= allowed
