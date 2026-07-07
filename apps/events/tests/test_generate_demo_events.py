"""ADR-0020 §4 — the dev-only demo events command."""

from datetime import timedelta
from io import StringIO

import pytest
from django.contrib.gis.geos import Point
from django.core.management import CommandError, call_command
from django.utils import timezone

from apps.events.models import Event
from apps.places.models import Place

pytestmark = pytest.mark.django_db


@pytest.fixture
def place():
    return Place.objects.create(
        name="Casa de Cultură", location=Point(23.59, 46.77, srid=4326), source=Place.Source.OSM
    )


def _past_event(place, days_ago=10, title="Concert vechi"):
    starts = timezone.now() - timedelta(days=days_ago)
    return Event.objects.create(
        title=title,
        starts_at=starts,
        ends_at=starts + timedelta(hours=2),
        source="roedu",
        external_id=f"roedu:{title}",
        place=place,
    )


def _run(*args, debug=True, settings=None):
    settings.DEBUG = debug
    out = StringIO()
    call_command("generate_demo_events", *args, stdout=out)
    return out.getvalue()


def test_refuses_outside_debug(settings, place):
    settings.DEBUG = False
    with pytest.raises(CommandError):
        call_command("generate_demo_events")


def test_reschedules_past_events_preserving_weekday_and_time(settings, place):
    event = _past_event(place, days_ago=17)
    old_weekday = event.starts_at.weekday()
    old_time = event.starts_at.time()

    _run(settings=settings)

    event.refresh_from_db()
    assert event.starts_at >= timezone.now()
    assert event.starts_at <= timezone.now() + timedelta(days=29)
    assert event.starts_at.weekday() == old_weekday
    assert event.starts_at.time() == old_time
    assert event.ends_at > event.starts_at


def test_synthesize_is_idempotent_and_marked(settings, place):
    _run("--synthesize", "6", settings=settings)
    first = list(Event.objects.filter(source="demo").values_list("external_id", flat=True))

    _run("--synthesize", "6", settings=settings)

    assert Event.objects.filter(source="demo").count() == len(first) == 6
    assert all(x.startswith("demo:") for x in first)
    assert Event.objects.filter(source="demo", starts_at__gte=timezone.now()).count() == 6
    titles = Event.objects.filter(source="demo").values_list("title", flat=True)
    assert all(t.startswith("[DEMO]") for t in titles)


def test_dry_run_changes_nothing(settings, place):
    event = _past_event(place)
    before = event.starts_at

    out = _run("--dry-run", "--synthesize", "3", settings=settings)

    event.refresh_from_db()
    assert event.starts_at == before
    assert Event.objects.filter(source="demo").count() == 0
    assert "would reschedule 1" in out
