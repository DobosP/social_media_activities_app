"""W9 aggregation prep: EventFeed sync (per-feed namespacing + failure isolation), the
M2M batch-events endpoint (admin-only, idempotent), and the match-place helper."""

from datetime import timedelta
from unittest import mock

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event, EventFeed
from apps.events.sources import RawEvent
from apps.places.models import Place

pytestmark = pytest.mark.django_db

ICS = (
    "BEGIN:VCALENDAR\r\nBEGIN:VEVENT\r\nUID:shared-uid-1\r\n"
    "DTSTART:20371003T180000Z\r\nSUMMARY:Chess night\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
)


def _user(name, *, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    if staff:
        u.is_staff = True
        u.save(update_fields=["is_staff"])
    return u


def test_sync_namespaces_external_ids_and_isolates_failures():
    good_a = EventFeed.objects.create(name="Library", url="https://example.org/a.ics")
    broken = EventFeed.objects.create(name="Broken", url="https://example.org/broken.ics")
    good_b = EventFeed.objects.create(name="Arts hall", url="https://example.org/b.ics")

    def fake_fetch(self):
        # Both good feeds emit the SAME UID — namespacing must keep them apart.
        if "broken" in self.url:
            raise OSError("connection refused")
        yield RawEvent(
            title=f"Chess night ({self.url[-5]})",
            starts_at=timezone.now() + timedelta(days=30),
            external_id="shared-uid-1",
        )

    with mock.patch("apps.events.sources.ICalFeedSource.fetch", fake_fetch, create=True):
        call_command("sync_event_feeds")

    assert Event.objects.count() == 2  # one per good feed, no UID collision
    external_ids = set(Event.objects.values_list("external_id", flat=True))
    assert external_ids == {f"feed{good_a.pk}:shared-uid-1", f"feed{good_b.pk}:shared-uid-1"}
    broken.refresh_from_db()
    assert broken.last_status.startswith("error:")
    good_a.refresh_from_db()
    assert good_a.last_status.startswith("ok:") and good_a.last_synced_at is not None

    # idempotent: a re-run upserts, never duplicates
    with mock.patch("apps.events.sources.ICalFeedSource.fetch", fake_fetch, create=True):
        call_command("sync_event_feeds")
    assert Event.objects.count() == 2


def test_batch_events_admin_only_and_idempotent():
    api = APIClient()
    api.force_authenticate(_user("agg-pleb"))
    assert api.post("/api/ingestion/batch-events/", [], format="json").status_code == 403

    api.force_authenticate(_user("agg-admin", staff=True))
    venue = Place.objects.create(
        name="Agg Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    rows = [
        {
            "title": "City run",
            "starts_at": (timezone.now() + timedelta(days=3)).isoformat(),
            "source": "manual",
            "external_id": "aggsys:run-1",
            "place_id": venue.pk,
        },
        {"title": "missing starts_at", "source": "manual"},
    ]
    resp = api.post("/api/ingestion/batch-events/", rows, format="json")
    assert resp.status_code == 200
    body = resp.json()
    assert body["processed"] == 1 and len(body["errors"]) == 1
    # idempotent on (source, external_id)
    api.post("/api/ingestion/batch-events/", rows[:1], format="json")
    assert Event.objects.filter(external_id="aggsys:run-1").count() == 1


def test_match_place_finds_canonical_venue():
    api = APIClient()
    api.force_authenticate(_user("agg-matcher", staff=True))
    venue = Place.objects.create(
        name="Central Sports Hall",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
    )
    found = api.get(
        "/api/ingestion/match-place/",
        {"lon": "23.6001", "lat": "46.7701", "name": "Central Sport Hall"},
    ).json()
    assert found["match"]["id"] == venue.pk
    nothing = api.get(
        "/api/ingestion/match-place/", {"lon": "23.0", "lat": "46.0", "name": "Elsewhere"}
    ).json()
    assert nothing["match"] is None
