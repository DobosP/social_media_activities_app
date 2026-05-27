from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event
from apps.events.services import import_events, upsert_event
from apps.events.sources import ICalFeedSource, RawEvent, parse_ics
from apps.places.models import Place

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


FUTURE = (timezone.now() + timedelta(days=3)).strftime("%Y%m%dT%H%M%SZ")
PAST = (timezone.now() - timedelta(days=3)).strftime("%Y%m%dT%H%M%SZ")

ICS = f"""BEGIN:VCALENDAR
BEGIN:VEVENT
UID:evt-1@venue
SUMMARY:Chess club night
DESCRIPTION:Weekly casual chess
DTSTART:{FUTURE}
DTEND:{FUTURE}
URL:https://venue.example.ro/chess
END:VEVENT
BEGIN:VEVENT
UID:evt-old@venue
SUMMARY:Past event
DTSTART:{PAST}
END:VEVENT
END:VCALENDAR
"""


def test_parse_ics_extracts_fields():
    events = parse_ics(ICS)
    assert len(events) == 2
    first = next(e for e in events if e.external_id == "evt-1@venue")
    assert first.title == "Chess club night"
    assert first.url == "https://venue.example.ro/chess"
    assert first.starts_at.tzinfo is not None


def test_ical_source_skips_past_events():
    fetched = list(ICalFeedSource(text=ICS).fetch())
    assert {e.external_id for e in fetched} == {"evt-1@venue"}


def test_line_unfolding():
    folded = (
        "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:u1\n"
        "SUMMARY:Long title that is\n  folded across lines\n"
        f"DTSTART:{FUTURE}\nEND:VEVENT\nEND:VCALENDAR\n"
    )
    events = parse_ics(folded)
    assert events[0].title == "Long title that is folded across lines"


def test_import_events_attaches_place_and_is_idempotent():
    place = Place.objects.create(
        name="City Library", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    n1 = import_events(ICalFeedSource(text=ICS), place=place)
    n2 = import_events(ICalFeedSource(text=ICS), place=place)
    assert n1 == 1 and n2 == 1
    assert Event.objects.count() == 1  # UID-keyed upsert, no duplication
    assert Event.objects.first().place == place


def test_upsert_without_uid_keys_on_place_title_start():
    place = Place.objects.create(
        name="Park", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    starts = timezone.now() + timedelta(days=1)
    raw = RawEvent(title="Picnic", starts_at=starts, source="user")
    upsert_event(raw, place=place, source="user")
    upsert_event(raw, place=place, source="user")
    assert Event.objects.filter(title="Picnic").count() == 1


def test_events_api_lists_upcoming_only():
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    import_events(ICalFeedSource(text=ICS), place=place)
    # Add a past event directly.
    Event.objects.create(
        title="Old", starts_at=timezone.now() - timedelta(days=1), source=Event.Source.MANUAL
    )

    client = APIClient()
    client.force_authenticate(_user("e1"))
    resp = client.get("/api/events/")
    assert resp.status_code == 200
    titles = [e["title"] for e in resp.json()["results"]]
    assert "Chess club night" in titles
    assert "Old" not in titles

    assert client.get("/api/events/?include_past=true").json()["count"] == 2
