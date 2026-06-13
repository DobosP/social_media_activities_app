"""F21 — event accuracy reports (ingest-safe, decaying overlay). Clones the F28 pattern."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event, EventReport
from apps.events.services import (
    clear_event_reports,
    event_is_flagged,
    event_reliability,
    file_event_report,
    upsert_event,
)
from apps.events.sources import RawEvent
from apps.places.models import Place

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
K = EventReport.Kind


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if band == AgeBand.UNDER_16:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _event(title="Chess night"):
    return Event.objects.create(
        title=title, starts_at=timezone.now() + timedelta(days=3), source=Event.Source.MANUAL
    )


def test_reliable_until_quorum():
    event = _event()
    assert event_reliability(event) is None
    file_event_report(_user("r1"), event, K.CANCELLED)
    file_event_report(_user("r2"), event, K.CANCELLED)
    assert event_is_flagged(event) is False  # 2 < quorum
    file_event_report(_user("r3"), event, K.MOVED)
    assert event_is_flagged(event) is True  # 3 reports (kinds may differ)
    assert event_reliability(event) == "unverified"


def test_reports_decay():
    event = _event()
    for i in range(3):
        file_event_report(_user(f"d{i}"), event, K.CANCELLED)
    assert event_is_flagged(event) is True
    # Age all reports past the decay window -> they stop counting (event self-heals).
    old = timezone.now() - timedelta(days=30)
    EventReport.objects.filter(event=event).update(created_at=old)
    assert event_is_flagged(event) is False
    assert event_reliability(event) is None


def test_idempotent_per_reporter_per_window():
    event = _event()
    u = _user("idem")
    assert file_event_report(u, event, K.CANCELLED) is not None
    assert file_event_report(u, event, K.MOVED) is None  # same window -> no second row
    assert EventReport.objects.filter(event=event, reporter=u).count() == 1


def test_requires_participation_and_known_kind():
    event = _event()
    unverified = User.objects.create_user(username="u0", password="pw")
    with pytest.raises(PermissionError):
        file_event_report(unverified, event, K.CANCELLED)
    with pytest.raises(ValueError):
        file_event_report(_user("badkind"), event, "exploded")


def test_rate_limited(settings):
    settings.EVENT_REPORT_RATE_LIMIT = 1
    settings.EVENT_REPORT_RATE_WINDOW_SECONDS = 3600
    u = _user("rl")
    assert file_event_report(u, _event("a"), K.CANCELLED) is not None
    assert file_event_report(u, _event("b"), K.CANCELLED) is None  # throttled across events


def test_child_and_adult_report_same_tally():
    # Documented decision: events are cohort-blind, so a CHILD and ADULT report into the SAME
    # event tally — acceptable (cancellation is cohort-neutral physical reality; counts-only).
    event = _event()
    file_event_report(_user("adult1"), event, K.CANCELLED)
    file_event_report(_user("adult2"), event, K.CANCELLED)
    file_event_report(_user("child1", band=AgeBand.UNDER_16), event, K.CANCELLED)
    assert event_is_flagged(event) is True  # all three count toward the same tally


def test_clear_reports_resets():
    event = _event()
    for i in range(3):
        file_event_report(_user(f"c{i}"), event, K.CANCELLED)
    assert event_is_flagged(event) is True
    clear_event_reports(event, moderator=_user("mod"))
    assert event_is_flagged(event) is False


def test_overlay_survives_reingest():
    # The report overlay must NOT be clobbered when the event is re-upserted from its feed.
    place = Place.objects.create(name="Hall", location=PT, source=Place.Source.OSM)
    raw = RawEvent(
        title="Recurring talk",
        starts_at=timezone.now() + timedelta(days=5),
        ends_at=None,
        external_id="feed:talk-1",
        url="",
        description="",
    )
    event = upsert_event(raw, source=Event.Source.ICAL, place=place)
    for i in range(3):
        file_event_report(_user(f"ri{i}"), event, K.CANCELLED)
    assert event_is_flagged(event) is True
    upsert_event(raw, source=Event.Source.ICAL, place=place)  # re-ingest same event
    event.refresh_from_db()
    assert event_is_flagged(event) is True  # reports survived
