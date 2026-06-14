"""F18 — getting_home_note field plumbing + draft seed (service/serializer layer).

The guardian-manifest mirror itself is covered by the web tests; here we pin that the one
net-new owner-curated field behaves exactly like the other F9 logistics fields (edit path,
length cap, serialized) and that draft_activity_text seeds a getting-home prompt for minors
only, never overwriting typed input.
"""

from datetime import timedelta

import pytest

from apps.accounts.models import Cohort
from apps.social.serializers import (
    LOGISTICS_FIELD_MAX_LENGTH,
    ActivitySerializer,
    ActivityUpdateSerializer,
)
from apps.social.services import (
    ACTIVITY_EDITABLE_FIELDS,
    create_activity,
    create_series,
    draft_activity_text,
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


def test_serializer_exposes_getting_home_note(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now, getting_home_note="walk together")
    data = ActivitySerializer(activity).data
    assert data["getting_home_note"] == "walk together"


def test_update_serializer_enforces_length_cap():
    over = ActivityUpdateSerializer(
        data={"getting_home_note": "x" * (LOGISTICS_FIELD_MAX_LENGTH + 1)}
    )
    assert not over.is_valid()
    assert "getting_home_note" in over.errors
    at_cap = ActivityUpdateSerializer(data={"getting_home_note": "x" * LOGISTICS_FIELD_MAX_LENGTH})
    assert at_cap.is_valid(), at_cap.errors


def test_series_carries_and_copies_getting_home_note(adult, place, activity_type, now):
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


# --- draft_activity_text seeding (minors only, never for adults) -----------------------


def test_draft_seeds_getting_home_note_for_child(activity_type):
    draft = draft_activity_text(activity_type=activity_type, cohort=Cohort.CHILD)
    assert draft["getting_home_note"]  # non-empty seed for a CHILD organiser


def test_draft_seeds_getting_home_note_for_teen(activity_type):
    draft = draft_activity_text(activity_type=activity_type, cohort=Cohort.TEEN)
    assert draft["getting_home_note"]


def test_draft_no_getting_home_note_for_adult(activity_type):
    draft = draft_activity_text(activity_type=activity_type, cohort=Cohort.ADULT)
    assert draft["getting_home_note"] == ""
