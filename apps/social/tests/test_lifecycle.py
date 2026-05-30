"""Activity lifecycle, in-place edit, and organiser announcements (starter-set F1/F2/F11)."""

from datetime import timedelta

import pytest
from django.core.management import call_command

from apps.notifications.models import Notification
from apps.social.models import Activity, Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    can_join,
    cancel_activity,
    create_activity,
    post_announcement,
    update_activity,
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


def _add_member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _url(activity):
    return f"/api/social/activities/{activity.id}/"


# --- F1: cancel -------------------------------------------------------------------------


def test_cancel_by_owner_sets_status_and_notifies_members(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _add_member(activity, adult2)
    cancel_activity(adult, activity, reason="court flooded")
    activity.refresh_from_db()
    assert activity.status == Activity.Status.CANCELLED
    note = Notification.objects.get(recipient=adult2, kind=Notification.Kind.ACTIVITY_CANCELLED)
    assert "court flooded" in note.body
    assert note.url == _url(activity)
    # The owner is not notified about their own cancellation.
    assert not Notification.objects.filter(
        recipient=adult, kind=Notification.Kind.ACTIVITY_CANCELLED
    ).exists()


def test_cancel_rejected_for_non_owner(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _add_member(activity, adult2)
    with pytest.raises(NotAMember):
        cancel_activity(adult2, activity)


def test_cancel_rejected_when_not_open(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    cancel_activity(adult, activity)
    with pytest.raises(InvalidState):
        cancel_activity(adult, activity)


def test_cancelled_activity_cannot_be_joined(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(hours=2))
    cancel_activity(adult, activity)
    joiner = make_user("latejoiner")
    assert can_join(joiner, activity) is False


# --- F1: auto-complete command ----------------------------------------------------------


def test_auto_complete_only_touches_past_open(adult, adult2, place, activity_type, now):
    past = _activity(adult, place, activity_type, now - timedelta(days=2))
    future = _activity(adult2, place, activity_type, now + timedelta(days=2))
    already_cancelled = _activity(adult, place, activity_type, now - timedelta(days=3))
    cancel_activity(adult, already_cancelled)

    call_command("auto_complete_activities")

    past.refresh_from_db()
    future.refresh_from_db()
    already_cancelled.refresh_from_db()
    assert past.status == Activity.Status.COMPLETED
    assert future.status == Activity.Status.OPEN  # still upcoming
    assert already_cancelled.status == Activity.Status.CANCELLED  # terminal, untouched


def test_auto_complete_respects_grace_window(adult, place, activity_type, now):
    # Started 2h ago: inside the default 12h grace, so it must remain OPEN.
    recent = _activity(adult, place, activity_type, now - timedelta(hours=2))
    call_command("auto_complete_activities")
    recent.refresh_from_db()
    assert recent.status == Activity.Status.OPEN


# --- F2: edit ---------------------------------------------------------------------------


def test_update_changes_editable_fields(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(days=1))
    update_activity(adult, activity, title="New title", description="bring water", capacity=8)
    activity.refresh_from_db()
    assert activity.title == "New title"
    assert activity.description == "bring water"
    assert activity.capacity == 8


def test_update_locked_fields_are_ignored(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(days=1))
    other = type(place).objects.create(
        name="Other place", location=place.location, source=place.source
    )
    # place is not an editable field; the change must be silently dropped (no bait-and-switch).
    update_activity(adult, activity, place=other, title="Renamed")
    activity.refresh_from_db()
    assert activity.place_id == place.id
    assert activity.title == "Renamed"


def test_update_time_change_notifies_and_supersedes_reminder(
    adult, adult2, place, activity_type, now
):
    activity = _activity(adult, place, activity_type, now + timedelta(hours=1))
    _add_member(activity, adult2)
    # A reminder already went out for the old time (dedup is on url, which has no time).
    Notification.objects.create(
        recipient=adult2, kind=Notification.Kind.EVENT_REMINDER, title="soon", url=_url(activity)
    )
    update_activity(adult, activity, starts_at=now + timedelta(hours=5))
    # Stale reminder cleared so the new time can re-fire; member told about the change.
    assert not Notification.objects.filter(
        recipient=adult2, kind=Notification.Kind.EVENT_REMINDER
    ).exists()
    assert Notification.objects.filter(
        recipient=adult2, kind=Notification.Kind.ACTIVITY_UPDATED
    ).exists()


def test_update_rejected_for_non_owner(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(days=1))
    _add_member(activity, adult2)
    with pytest.raises(NotAMember):
        update_activity(adult2, activity, title="hijack")


def test_update_rejected_after_start(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now - timedelta(minutes=5))
    with pytest.raises(InvalidState):
        update_activity(adult, activity, title="too late")


def test_update_capacity_below_participants_rejected(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(days=1), capacity=5)
    _add_member(activity, adult2)  # owner + adult2 = 2 participants
    with pytest.raises(InvalidState):
        update_activity(adult, activity, capacity=1)


def test_update_end_before_start_rejected(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now + timedelta(days=1))
    with pytest.raises(InvalidState):
        update_activity(adult, activity, ends_at=now)  # before the (future) start


# --- F11: announcements -----------------------------------------------------------------


def test_announcement_flags_post_and_notifies_members(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _add_member(activity, adult2)
    post = post_announcement(adult, activity, "Meet at the north gate at 5.")
    assert post.is_announcement is True
    note = Notification.objects.get(recipient=adult2, kind=Notification.Kind.ANNOUNCEMENT)
    assert "north gate" in note.body
    assert note.url == _url(activity)


def test_announcement_rejected_for_non_owner(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _add_member(activity, adult2)
    with pytest.raises(NotAMember):
        post_announcement(adult2, activity, "not allowed")
