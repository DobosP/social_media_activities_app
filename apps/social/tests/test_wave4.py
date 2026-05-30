"""F22: post-meetup 'did we meet?' closing signal (per-activity, no per-person rating)."""

import pytest

from apps.social.models import Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    NotEligible,
    add_guardian,
    complete_activity,
    create_activity,
    leave_activity,
    met_confirmation_summary,
    set_met_confirmed,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, **kw):
    return create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=now, **kw
    )


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def test_cannot_confirm_before_completed(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)  # OPEN
    with pytest.raises(InvalidState):
        set_met_confirmed(adult, activity)


def test_confirm_and_summary(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    complete_activity(activity)
    set_met_confirmed(adult2, activity)
    assert met_confirmation_summary(activity) == {"confirmed": 1, "total": 2}


def test_confirm_is_idempotent(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    complete_activity(activity)
    first = set_met_confirmed(adult, activity)
    stamp = first.met_confirmed_at
    second = set_met_confirmed(adult, activity)
    assert second.met_confirmed_at == stamp  # unchanged; no re-fire
    assert met_confirmation_summary(activity)["confirmed"] == 1


def test_undo_confirmation(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    complete_activity(activity)
    set_met_confirmed(adult, activity)
    set_met_confirmed(adult, activity, confirmed=False)
    assert activity.memberships.get(user=adult).met_confirmed_at is None
    assert met_confirmation_summary(activity)["confirmed"] == 0


def test_non_member_cannot_confirm(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    complete_activity(activity)
    outsider = make_user("metoutsider")
    with pytest.raises(NotAMember):
        set_met_confirmed(outsider, activity)


def test_guardian_cannot_confirm(child, place, activity_type, now):
    guardian = make_user("metguardian")
    from apps.accounts.services import link_guardian

    link_guardian(guardian, child)
    activity = _activity(child, place, activity_type, now, guardian_accompanied=True)
    add_guardian(child, activity, guardian)
    complete_activity(activity)
    with pytest.raises(NotEligible):
        set_met_confirmed(guardian, activity)
    # The guardian is also excluded from the denominator (participants only).
    assert met_confirmation_summary(activity)["total"] == 1


def test_leaving_clears_met_confirmation(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    m = _member(activity, adult2)
    complete_activity(activity)
    set_met_confirmed(adult2, activity)
    leave_activity(adult2, activity)
    m.refresh_from_db()
    assert m.met_confirmed_at is None  # a removed row carries no signal


def test_confirmation_isolated_across_activities(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now)
    b = _activity(adult, place, activity_type, now)
    complete_activity(a)
    complete_activity(b)
    set_met_confirmed(adult, a)
    assert met_confirmation_summary(a) == {"confirmed": 1, "total": 1}
    assert met_confirmation_summary(b) == {"confirmed": 0, "total": 1}  # never rolled up per-user
