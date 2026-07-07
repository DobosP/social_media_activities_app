"""W3-F8 — fallback_meeting_point: a member-only plan-B gathering spot WITHIN the known venue.

Mirrors the getting_home_note / first_time_note member-only logistics tier: editable + creatable
on the same path, length-capped on every write serializer, deliberately NOT echoed by the
cohort-wide read serializer (so it can't widen a minor's location surface), and folded into the
member-only reminder body. The CHILD guardian-manifest mirror is covered by the web tests.
"""

from datetime import timedelta

import pytest

from apps.social.serializers import (
    LOGISTICS_FIELD_MAX_LENGTH,
    ActivityCreateSerializer,
    ActivitySerializer,
    ActivityUpdateSerializer,
)
from apps.social.services import ACTIVITY_EDITABLE_FIELDS, create_activity, update_activity

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


def test_fallback_is_creatable_and_editable(adult, place, activity_type, now):
    assert "fallback_meeting_point" in ACTIVITY_EDITABLE_FIELDS
    a = _activity(adult, place, activity_type, now, fallback_meeting_point="Covered pavilion")
    assert a.fallback_meeting_point == "Covered pavilion"
    update_activity(adult, a, fallback_meeting_point="Secondary court if the main one is wet")
    a.refresh_from_db()
    assert a.fallback_meeting_point == "Secondary court if the main one is wet"


def test_read_serializer_does_not_expose_fallback(adult, place, activity_type, now):
    # Member-only: it must NOT ride the cohort-wide read serializer (any same-cohort viewer can
    # fetch that), mirroring getting_home_note — it widens a minor's location surface otherwise.
    a = _activity(adult, place, activity_type, now, fallback_meeting_point="pavilion by the gate")
    assert "fallback_meeting_point" not in ActivitySerializer(a).data


def test_write_serializers_enforce_length_cap():
    over = "x" * (LOGISTICS_FIELD_MAX_LENGTH + 1)
    for ser_cls in (ActivityCreateSerializer, ActivityUpdateSerializer):
        ser = ser_cls(data={"fallback_meeting_point": over})
        assert not ser.is_valid()
        assert "fallback_meeting_point" in ser.errors, ser_cls.__name__
    at_cap = ActivityUpdateSerializer(
        data={"fallback_meeting_point": "x" * LOGISTICS_FIELD_MAX_LENGTH}
    )
    assert at_cap.is_valid(), at_cap.errors


def test_reminder_body_omits_retired_fallback_line(adult, place, activity_type, now):
    """ADR-0019 §4: the Plan-B spot was retired from the product; a legacy value stored on
    an old activity must no longer leak into member reminders."""
    from apps.notifications.management.commands.send_activity_reminders import _reminder_body

    a = _activity(adult, place, activity_type, now, fallback_meeting_point="the foyer if it rains")
    body = _reminder_body(a)
    assert "the foyer if it rains" not in body
    assert "Plan B" not in body


def test_routine_edit_omitting_fallback_does_not_wipe_it(adult, place, activity_type, now):
    # The update serializer has no default for fallback_meeting_point, so a partial PATCH that
    # omits it leaves the stored value intact (the web edit form prefills it for the same reason).
    a = _activity(adult, place, activity_type, now, fallback_meeting_point="pavilion")
    update_activity(adult, a, title="Renamed hike")  # edit something else, omit the fallback
    a.refresh_from_db()
    assert a.fallback_meeting_point == "pavilion"
