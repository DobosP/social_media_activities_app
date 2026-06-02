"""Wave-2 features: arrival ping (F3), RSVP intent (F20), logistics card (F9)."""

from datetime import timedelta

import pytest
from django.core.management import call_command

from apps.accounts.services import link_guardian
from apps.notifications.models import Notification
from apps.safety.services import block_user
from apps.social.models import Activity, Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    add_guardian,
    arrival_window_open,
    attendance_summary,
    cancel_activity,
    create_activity,
    leave_activity,
    mark_arrived,
    set_attendance_intent,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, starts_at, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Pickup game",
        starts_at=starts_at,
        **kw,
    )


def _member(activity, user, role=Membership.Role.MEMBER):
    return activity.memberships.create(user=user, role=role, state=Membership.State.MEMBER)


def _arrivals_to(user):
    return Notification.objects.filter(recipient=user, kind=Notification.Kind.ARRIVAL)


# --- F3: arrival ping ------------------------------------------------------------------


def test_mark_arrived_sets_timestamp_and_notifies_other_members(
    adult, adult2, place, activity_type, now
):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    membership = mark_arrived(adult, activity)
    assert membership.arrived_at is not None
    assert _arrivals_to(adult2).count() == 1  # the other member is told
    assert _arrivals_to(adult).count() == 0  # the arriver is not pinged


def test_mark_arrived_body_carries_only_display_name_and_title(
    adult, adult2, place, activity_type, now
):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    mark_arrived(adult, activity)
    note = _arrivals_to(adult2).get()
    # The only arriver-derived string is display_name; there is no per-ping note channel.
    assert adult.display_name in note.body
    assert activity.title in note.body


def test_mark_arrived_child_also_notifies_guardian(child, place, activity_type, now):
    guardian = make_user("guardian1")  # ADULT
    link_guardian(guardian, child)
    activity = _activity(child, place, activity_type, now)  # CHILD cohort, child is owner-member
    mark_arrived(child, activity)
    assert _arrivals_to(guardian).count() == 1


def test_mark_arrived_guardian_member_gets_single_ping(child, place, activity_type, now):
    guardian = make_user("guardian2")
    link_guardian(guardian, child)
    activity = _activity(child, place, activity_type, now, guardian_accompanied=True)
    add_guardian(child, activity, guardian)  # guardian is now a GUARDIAN-role current member
    mark_arrived(child, activity)
    # Guardian is both a current member and an active guardian → exactly one ARRIVAL.
    assert _arrivals_to(guardian).count() == 1


def test_mark_arrived_is_idempotent(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    mark_arrived(adult, activity)
    mark_arrived(adult, activity)  # second tap
    assert _arrivals_to(adult2).count() == 1  # not re-pinged


def test_mark_arrived_excludes_blocked_pair(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    block_user(adult2, adult)  # adult2 blocked the arriver
    mark_arrived(adult, activity)
    assert _arrivals_to(adult2).count() == 0


def test_mark_arrived_outside_window_rejected(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(hours=6))
    assert arrival_window_open(activity) is False
    with pytest.raises(InvalidState):
        mark_arrived(adult, activity)


def test_mark_arrived_non_member_rejected(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    outsider = make_user("outsider")
    with pytest.raises(NotAMember):
        mark_arrived(outsider, activity)


def test_mark_arrived_rejected_when_not_open(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    cancel_activity(adult, activity)
    with pytest.raises(InvalidState):
        mark_arrived(adult, activity)


def test_expire_arrivals_clears_old_pings(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    mark_arrived(adult, activity)
    # Shift the activity's start well into the past so it is beyond the retention window.
    Activity.objects.filter(pk=activity.pk).update(starts_at=now - timedelta(hours=12))
    call_command("expire_arrivals")
    assert activity.memberships.get(user=adult).arrived_at is None


# --- F20: RSVP attendance intent -------------------------------------------------------


def test_set_attendance_intent_and_summary(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    set_attendance_intent(adult2, activity, Membership.AttendanceIntent.GOING)
    summary = attendance_summary(activity)
    # owner + adult2 are the participants; F1 quorum keys are None with no threshold set.
    assert summary["going"] == 1 and summary["total"] == 2
    assert summary["min_to_go"] is None and summary["met_minimum"] is None


def test_invalid_attendance_intent_rejected(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    with pytest.raises(InvalidState):
        set_attendance_intent(adult, activity, "definitely")


def test_set_attendance_intent_non_member_rejected(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    outsider = make_user("rsvpoutsider")
    with pytest.raises(NotAMember):
        set_attendance_intent(outsider, activity, Membership.AttendanceIntent.GOING)


def test_leave_resets_attendance_intent(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    m = _member(activity, adult2)
    set_attendance_intent(adult2, activity, Membership.AttendanceIntent.GOING)
    leave_activity(adult2, activity)
    m.refresh_from_db()
    # Leaving literally erases the go/no-go datum — it does not linger on the removed row.
    assert m.attendance_intent == Membership.AttendanceIntent.UNKNOWN


def test_attendance_intent_isolated_across_activities(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now)
    b = _activity(adult, place, activity_type, now)
    set_attendance_intent(adult, a, Membership.AttendanceIntent.GOING)
    sa, sb = attendance_summary(a), attendance_summary(b)
    assert (sa["going"], sa["total"]) == (1, 1)
    assert (sb["going"], sb["total"]) == (0, 1)  # B unaffected


def test_attendance_summary_excludes_guardians(child, place, activity_type, now):
    guardian = make_user("rsvpguardian")
    link_guardian(guardian, child)
    activity = _activity(child, place, activity_type, now, guardian_accompanied=True)
    add_guardian(child, activity, guardian)
    # Only the child is a participant; the supervisory guardian is excluded from the count.
    s = attendance_summary(activity)
    assert s["going"] == 0 and s["total"] == 1


# --- F9: logistics ---------------------------------------------------------------------


def test_create_activity_with_logistics(adult, place, activity_type, now):
    activity = _activity(
        adult, place, activity_type, now, meeting_point="North gate", what_to_bring="Water"
    )
    assert activity.meeting_point == "North gate"
    assert activity.what_to_bring == "Water"


def test_update_activity_edits_logistics(adult, place, activity_type, now):
    from apps.social.services import update_activity

    activity = _activity(adult, place, activity_type, now + timedelta(days=1))
    update_activity(adult, activity, meeting_point="By the fountain", organizer_note="Wear red")
    activity.refresh_from_db()
    assert activity.meeting_point == "By the fountain"
    assert activity.organizer_note == "Wear red"
