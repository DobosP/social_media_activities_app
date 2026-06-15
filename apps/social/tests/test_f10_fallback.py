"""W2-F10: plan-B fallback meetup time.

A single owner-curated backup start that invoke_fallback consumes ONCE through the update_activity
time-change path (member re-notify + reminder supersede inherited) and audits the shift. One-use
latch: the backup is cleared in the same transaction, so it can never loop into an open-ended
reschedule.
"""

from datetime import timedelta

import pytest

from apps.notifications.models import Notification
from apps.safety.models import AuditLog
from apps.social.models import Activity, Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    cancel_activity,
    create_activity,
    grant_co_organizer,
    invoke_fallback,
    update_activity,
)

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, *, fallback_hours=5, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Hike",
        starts_at=now + timedelta(hours=2),
        fallback_starts_at=(now + timedelta(hours=fallback_hours)) if fallback_hours else None,
        **kw,
    )


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def test_invoke_fallback_shifts_time_clears_latch_and_notifies(
    adult, adult2, place, activity_type, now
):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    target = activity.fallback_starts_at
    invoke_fallback(adult, activity)
    activity.refresh_from_db()
    assert activity.starts_at == target  # moved to the plan-B time
    assert activity.fallback_starts_at is None  # one-use latch consumed
    # The member was re-notified of the new time via the update_activity path.
    assert Notification.objects.filter(
        recipient=adult2, kind=Notification.Kind.ACTIVITY_UPDATED
    ).exists()


def test_invoke_fallback_is_one_use(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    invoke_fallback(adult, activity)
    activity.refresh_from_db()
    with pytest.raises(InvalidState):  # latch is gone -> "no plan-B time set"
        invoke_fallback(adult, activity)


def test_invoke_fallback_requires_a_backup_set(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now, fallback_hours=None)
    with pytest.raises(InvalidState):
        invoke_fallback(adult, activity)


def test_invoke_fallback_rejects_past_backup(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    Activity.objects.filter(pk=activity.pk).update(fallback_starts_at=now - timedelta(hours=1))
    activity.refresh_from_db()
    with pytest.raises(InvalidState):
        invoke_fallback(adult, activity)


def test_invoke_fallback_rejects_a_plain_member(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    with pytest.raises(NotAMember):
        invoke_fallback(adult2, activity)  # a plain member is not the organiser


def test_invoke_fallback_allows_a_co_organizer(adult, adult2, place, activity_type, now):
    # Organiser-only = owner OR an F22 co-organiser (adult activities), mirroring cancel/edit.
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    grant_co_organizer(adult, activity, adult2)
    target = activity.fallback_starts_at
    invoke_fallback(adult2, activity)  # the co-organiser may use the plan-B time
    activity.refresh_from_db()
    assert activity.starts_at == target


def test_invoke_fallback_is_audited(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    invoke_fallback(adult, activity)
    assert AuditLog.objects.filter(
        event="activity.fallback_invoked", target_ref=f"social.activity:{activity.pk}"
    ).exists()


def test_invoke_fallback_rejected_when_not_open(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    cancel_activity(adult, activity)
    with pytest.raises(InvalidState):
        invoke_fallback(adult, activity)


def test_invoke_fallback_rolls_back_when_original_already_started(adult, place, activity_type, now):
    # If the ORIGINAL start has passed, update_activity refuses the edit; the atomic block then
    # rolls the latch back too, so the backup is NOT silently consumed.
    activity = _activity(adult, place, activity_type, now)
    Activity.objects.filter(pk=activity.pk).update(starts_at=now - timedelta(hours=1))
    activity.refresh_from_db()
    with pytest.raises(InvalidState):
        invoke_fallback(adult, activity)
    activity.refresh_from_db()
    assert activity.fallback_starts_at is not None  # latch preserved on rollback


def test_fallback_is_an_editable_field(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now, fallback_hours=None)
    new_backup = now + timedelta(hours=6)
    update_activity(adult, activity, fallback_starts_at=new_backup)
    activity.refresh_from_db()
    assert activity.fallback_starts_at == new_backup


def test_service_rejects_fallback_at_or_before_start_on_create_and_edit(
    adult, place, activity_type, now
):
    # The "plan-B must be after the planned start" rule is centralised in the service, so BOTH the
    # web form and the DRF serializers inherit it (the web form also field-validates).
    with pytest.raises(InvalidState):
        create_activity(
            adult,
            place=place,
            activity_type=activity_type,
            title="Early",
            starts_at=now + timedelta(hours=2),
            fallback_starts_at=now + timedelta(hours=1),  # before the start
        )
    activity = _activity(adult, place, activity_type, now, fallback_hours=None)
    with pytest.raises(InvalidState):
        update_activity(adult, activity, fallback_starts_at=activity.starts_at)  # equal to start
