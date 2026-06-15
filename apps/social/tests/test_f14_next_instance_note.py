"""W2-F14: a one-shot "heads-up for the next meetup" appended to ONLY the next spawned instance's
organiser note, then auto-cleared (consume-on-spawn). Owner-scoped; capped at the model layer."""

from datetime import timedelta

import pytest

from apps.social import services as social
from apps.social.models import Activity, ActivitySeries
from apps.social.services import (
    InvalidState,
    NotAMember,
    set_next_instance_note,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _series(owner, place, activity_type, first_starts_at):
    return social.create_series(
        owner,
        place=place,
        activity_type=activity_type,
        title="Tuesday run",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=first_starts_at,
        organizer_note="Standing note: meet by the gate.",
    )


def test_set_next_instance_note_owner_only(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    other = make_user("not_the_owner")
    with pytest.raises(NotAMember):
        set_next_instance_note(other, s, "back pitch this time")


def test_set_next_instance_note_refused_on_ended_series(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    social.end_series(adult, s)
    with pytest.raises(InvalidState):
        set_next_instance_note(adult, s, "too late")


def test_set_next_instance_note_caps_length(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    set_next_instance_note(adult, s, "x" * 9000)
    s.refresh_from_db()
    assert len(s.next_instance_note) == 500  # model/service cap


def test_note_appended_to_next_instance_then_cleared(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    set_next_instance_note(adult, s, "Back pitch this time, bring cleats.")
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    # The standing template note is PRESERVED and the one-shot note APPENDED (never replaced).
    assert "Standing note: meet by the gate." in a.organizer_note
    assert "Back pitch this time, bring cleats." in a.organizer_note
    # Consumed: the series no longer carries the one-shot note.
    s.refresh_from_db()
    assert s.next_instance_note == ""


def test_note_lands_on_exactly_one_instance(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    set_next_instance_note(adult, s, "ONE_SHOT_MARKER")
    social.spawn_due_series(now=now)  # instance 1 @ now+1d, cursor -> +1 week
    first = Activity.objects.get(series=s)
    assert "ONE_SHOT_MARKER" in first.organizer_note
    # Advance past the first instance so a second can spawn.
    later = now + timedelta(days=8)
    Activity.objects.filter(pk=first.pk).update(starts_at=now - timedelta(hours=1))
    social.spawn_due_series(now=later)
    second = Activity.objects.filter(series=s).exclude(pk=first.pk).first()
    assert second is not None
    assert "ONE_SHOT_MARKER" not in second.organizer_note  # only the first instance carried it
    assert "Standing note: meet by the gate." in second.organizer_note  # template still carried


def test_note_survives_a_skipped_tick(adult, place, activity_type, now):
    # The note must survive a tick that processes the series but DOESN'T spawn (here: the
    # one-upcoming guard while instance 1 is still live), then land on the NEXT real spawn.
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    social.spawn_due_series(now=now)  # instance 1 (nothing staged yet)
    first = Activity.objects.get(series=s)
    set_next_instance_note(adult, s, "SURVIVES")
    social.spawn_due_series(now=now + timedelta(hours=6))  # one-upcoming guard -> no spawn
    s.refresh_from_db()
    assert s.next_instance_note == "SURVIVES"  # not lost on the skipped tick
    assert Activity.objects.filter(series=s).count() == 1
    # Free the slot so a second instance can spawn; the staged note now lands and clears.
    Activity.objects.filter(pk=first.pk).update(starts_at=now - timedelta(hours=1))
    social.spawn_due_series(now=now + timedelta(days=8))
    second = Activity.objects.filter(series=s).exclude(pk=first.pk).get()
    assert "SURVIVES" in second.organizer_note
    s.refresh_from_db()
    assert s.next_instance_note == ""


def test_no_note_means_plain_template_note(adult, place, activity_type, now):
    s = _series(adult, place, activity_type, now + timedelta(days=1))
    social.spawn_due_series(now=now)
    a = Activity.objects.get(series=s)
    assert a.organizer_note == "Standing note: meet by the gate."  # no stray separator/whitespace
