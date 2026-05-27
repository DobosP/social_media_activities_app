from datetime import timedelta

import pytest
from django.utils import timezone

from apps.events.classify import classify_activity
from apps.events.models import Event
from apps.events.services import import_events
from apps.events.sources import ICalFeedSource

pytestmark = pytest.mark.django_db


def test_classify_uses_taxonomy_aliases():
    # Seeded taxonomy (migration 0004) provides these slugs/aliases.
    assert classify_activity("Maratonul Internațional Cluj").slug == "marathon"
    assert classify_activity("Zilele Clujului – city day").slug == "city_day"
    assert classify_activity("Concert live în parc").slug == "concert"
    assert classify_activity("Atelier de pictura").slug == "workshop"


def test_classify_returns_none_when_no_match():
    assert classify_activity("Random unrelated text xyz") is None
    assert classify_activity("") is None


def _ics(summary: str) -> str:
    start = (timezone.now() + timedelta(days=2)).strftime("%Y%m%dT%H%M%SZ")
    return (
        "BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:c1\n"
        f"SUMMARY:{summary}\nDTSTART:{start}\nEND:VEVENT\nEND:VCALENDAR\n"
    )


def test_import_events_auto_classifies():
    import_events(ICalFeedSource(text=_ics("Festival de muzică")))
    event = Event.objects.get()
    assert event.activity_type is not None
    assert event.activity_type.slug == "festival"


def test_import_events_respects_explicit_activity_type():
    from apps.taxonomy.models import ActivityType

    running = ActivityType.objects.get(slug="running")
    import_events(ICalFeedSource(text=_ics("Some generic gathering")), activity_type=running)
    assert Event.objects.get().activity_type == running
