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


# --- W2-F6: bounded RRULE expansion ----------------------------------------------------------

from datetime import UTC, datetime  # noqa: E402

from apps.events.sources import _expand_rrule  # noqa: E402

_NOW = datetime(2026, 6, 15, 12, 0, 0, tzinfo=UTC)  # a Monday


def _recurring_ics(rrule, *, dtstart="20260616T180000Z", uid="rec@venue", dtend=None):
    end = f"DTEND:{dtend}\n" if dtend else ""
    return (
        "BEGIN:VCALENDAR\nBEGIN:VEVENT\n"
        f"UID:{uid}\nSUMMARY:Chess club\nDTSTART:{dtstart}\n{end}RRULE:{rrule}\n"
        "END:VEVENT\nEND:VCALENDAR\n"
    )


def test_rrule_weekly_count_expands_to_distinct_occurrences():
    events = parse_ics(_recurring_ics("FREQ=WEEKLY;COUNT=4"), now=_NOW)
    assert len(events) == 4
    days = sorted(e.starts_at.strftime("%Y%m%d") for e in events)
    assert days == ["20260616", "20260623", "20260630", "20260707"]
    # Per-occurrence external_ids are distinct (and stable across re-ingest).
    assert len({e.external_id for e in events}) == 4
    assert all(e.external_id.startswith("rec@venue:") for e in events)


def test_rrule_weekly_forever_is_bounded_to_horizon():
    events = parse_ics(_recurring_ics("FREQ=WEEKLY"), now=_NOW)  # no COUNT/UNTIL
    # ~90-day window from now => ~13 weekly occurrences, never unbounded.
    assert 10 <= len(events) <= 14
    assert all(e.starts_at <= _NOW.replace(microsecond=0) + timedelta(days=91) for e in events)


def test_rrule_until_caps_expansion():
    events = parse_ics(_recurring_ics("FREQ=WEEKLY;UNTIL=20260623T235959Z"), now=_NOW)
    days = sorted(e.starts_at.strftime("%Y%m%d") for e in events)
    assert days == ["20260616", "20260623"]


def test_rrule_interval_spacing():
    events = parse_ics(_recurring_ics("FREQ=WEEKLY;INTERVAL=2;COUNT=3"), now=_NOW)
    days = sorted(e.starts_at.strftime("%Y%m%d") for e in events)
    assert days == ["20260616", "20260630", "20260714"]


def test_rrule_weekly_byday_multiple_per_week():
    # DTSTART on Monday 2026-06-15; BYDAY=MO,WE.
    events = parse_ics(
        _recurring_ics("FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4", dtstart="20260615T180000Z"), now=_NOW
    )
    days = sorted(e.starts_at.strftime("%Y%m%d") for e in events)
    assert days == ["20260615", "20260617", "20260622", "20260624"]


def test_rrule_monthly_and_daily():
    monthly = parse_ics(_recurring_ics("FREQ=MONTHLY;COUNT=3"), now=_NOW)
    assert sorted(e.starts_at.strftime("%Y%m%d") for e in monthly) == [
        "20260616",
        "20260716",
        "20260816",
    ]
    daily = parse_ics(_recurring_ics("FREQ=DAILY;COUNT=3"), now=_NOW)
    assert sorted(e.starts_at.strftime("%Y%m%d") for e in daily) == [
        "20260616",
        "20260617",
        "20260618",
    ]


def test_rrule_preserves_duration():
    events = parse_ics(_recurring_ics("FREQ=WEEKLY;COUNT=2", dtend="20260616T200000Z"), now=_NOW)
    for e in events:
        assert (e.ends_at - e.starts_at) == timedelta(hours=2)


def test_unsupported_rrule_degrades_to_single_event():
    events = parse_ics(_recurring_ics("FREQ=YEARLY;COUNT=5"), now=_NOW)
    assert len(events) == 1


def test_non_recurring_event_keeps_bare_uid():
    # Regression: a VEVENT with no RRULE is unchanged (bare UID, no :date suffix).
    ics = (
        "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:plain@venue\nSUMMARY:One-off\n"
        "DTSTART:20260616T180000Z\nEND:VEVENT\nEND:VCALENDAR\n"
    )
    events = parse_ics(ics, now=_NOW)
    assert len(events) == 1 and events[0].external_id == "plain@venue"


def test_rrule_occurrence_external_id_fits_column():
    events = parse_ics(_recurring_ics("FREQ=WEEKLY;COUNT=3", uid="x" * 200), now=_NOW)
    assert all(len(e.external_id) <= 200 for e in events)  # Event.external_id max_length=200
    assert len({e.external_id for e in events}) == 3


def test_expand_rrule_iteration_guarded_against_far_past_unbounded():
    # A daily series from years ago with no end must not iterate unbounded / explode.
    past = _NOW - timedelta(days=30)
    occ = _expand_rrule(past, "FREQ=DAILY", now=_NOW)
    assert len(occ) <= 120  # capped; never the whole infinite tail
