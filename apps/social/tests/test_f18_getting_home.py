"""F18 — getting_home_note field plumbing (service/serializer layer).

The guardian-manifest mirror itself is covered by the web tests; here we pin that the one
net-new owner-curated field behaves like the other F9 logistics fields on the edit path and is
length-capped on every write serializer, that it is deliberately NOT echoed by the cohort-wide
read serializers (member-only, like the web card), that recurring series copy it to spawned
instances, and that draft_activity_text does NOT seed it (a template prompt must never be
mirrored to a guardian as if it were the organiser's real plan).
"""

from datetime import timedelta

import pytest

from apps.accounts.models import Cohort
from apps.social.models import Activity
from apps.social.serializers import (
    LOGISTICS_FIELD_MAX_LENGTH,
    ActivityCreateSerializer,
    ActivitySerializer,
    ActivityUpdateSerializer,
    SeriesCreateSerializer,
)
from apps.social.services import (
    ACTIVITY_EDITABLE_FIELDS,
    create_activity,
    create_series,
    draft_activity_text,
    spawn_due_series,
    update_activity,
)

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Hike",
        starts_at=now + timedelta(days=1),
        **kw,
    )


def test_getting_home_note_is_editable(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    update_activity(adult, activity, getting_home_note="Bus 25 from the north gate")
    activity.refresh_from_db()
    assert activity.getting_home_note == "Bus 25 from the north gate"


def test_getting_home_note_in_editable_fields_and_create(adult, place, activity_type, now):
    assert "getting_home_note" in ACTIVITY_EDITABLE_FIELDS
    activity = _activity(adult, place, activity_type, now, getting_home_note="Pickup at 8pm")
    assert activity.getting_home_note == "Pickup at 8pm"


def test_read_serializer_does_not_expose_getting_home_note(adult, place, activity_type, now):
    # Member-only: the child-safety-sensitive getting-home plan must NOT ride the cohort-wide
    # read serializer (which any same-cohort viewer can fetch). It is shown member-only on web.
    activity = _activity(adult, place, activity_type, now, getting_home_note="walk together")
    assert "getting_home_note" not in ActivitySerializer(activity).data


def test_all_write_serializers_enforce_length_cap():
    over = "x" * (LOGISTICS_FIELD_MAX_LENGTH + 1)
    for ser_cls in (ActivityCreateSerializer, ActivityUpdateSerializer, SeriesCreateSerializer):
        ser = ser_cls(data={"getting_home_note": over})
        assert not ser.is_valid()
        assert "getting_home_note" in ser.errors, ser_cls.__name__
    at_cap = ActivityUpdateSerializer(data={"getting_home_note": "x" * LOGISTICS_FIELD_MAX_LENGTH})
    assert at_cap.is_valid(), at_cap.errors


def test_series_copies_getting_home_note_to_spawned_instance(adult, place, activity_type, now):
    series = create_series(
        adult,
        place=place,
        activity_type=activity_type,
        title="Weekly run",
        first_starts_at=now + timedelta(days=1),
        cadence="weekly",
        getting_home_note="meet parents at the gate",
    )
    assert series.getting_home_note == "meet parents at the gate"
    # The headline F18 guarantee for recurring meetups: each spawned instance inherits the note.
    spawn_due_series(now=now)
    instance = Activity.objects.get(series=series)
    assert instance.getting_home_note == "meet parents at the gate"


# --- draft_activity_text must NOT seed getting_home_note (no fake plan on the manifest) ----


@pytest.mark.parametrize("cohort", [Cohort.CHILD, Cohort.TEEN, Cohort.ADULT])
def test_draft_does_not_seed_getting_home_note(activity_type, cohort):
    draft = draft_activity_text(activity_type=activity_type, cohort=cohort)
    assert set(draft.keys()) == {"title", "description"}
    assert "getting_home_note" not in draft
