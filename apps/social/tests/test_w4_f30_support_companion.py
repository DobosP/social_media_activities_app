"""W4-F30: a member's non-capacity-counted support-person companion flag. ADULTS-ONLY at launch;
never aggregated per-user; surfaced ONLY to the organiser as a logistical count; reset on leave.
"""

from datetime import timedelta

import pytest
from django.test import Client

from apps.social.models import Membership
from apps.social.services import (
    NotEligible,
    can_join,
    create_activity,
    leave_activity,
    organizer_console,
    participant_count,
    set_support_companion,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=now + timedelta(days=1),
        **kw,
    )


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def test_set_support_companion_sets_and_is_idempotent(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    m = set_support_companion(adult, activity, True)
    assert m.brings_support_person is True
    again = set_support_companion(adult, activity, True)  # idempotent no-op
    assert again.brings_support_person is True
    set_support_companion(adult, activity, False)
    assert activity.memberships.get(user=adult).brings_support_person is False


def test_rejected_for_non_adult_cohort(child, place, activity_type, now):
    # ADULTS-ONLY at launch (defence-in-depth; a CHILD activity owner is auto-seated MEMBER).
    activity = _activity(child, place, activity_type, now)
    with pytest.raises(NotEligible):
        set_support_companion(child, activity, True)


def test_support_companion_never_consumes_capacity(adult, adult2, place, activity_type, now):
    third = make_user("f30_third")
    activity = _activity(adult, place, activity_type, now, capacity=3)
    _member(activity, adult2)  # 2 member-positions of 3
    set_support_companion(adult, activity, True)
    set_support_companion(adult2, activity, True)  # 2 support persons, neither counted
    assert participant_count(activity) == 2  # MEMBER positions only — companions never counted
    assert can_join(third, activity) is True  # a 3rd member can still join (not "full" at 2+2)


def test_leave_resets_support_flag(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    set_support_companion(adult2, activity, True)
    left = leave_activity(adult2, activity)
    assert left.brings_support_person is False  # transient — cleared on leave


def test_organizer_console_shows_support_count(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    set_support_companion(adult2, activity, True)
    console = organizer_console(adult)
    row = next(r for r in console["activities"] if r["activity"].id == activity.id)
    assert row["support_companions"] == 1


def test_web_adult_member_toggles_support_person(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    _member(activity, adult2)
    c = Client()
    c.force_login(adult2)
    # The affordance is offered to an adult member.
    assert "bringing a support person" in c.get(f"/activities/{activity.id}/").content.decode()
    resp = c.post(f"/activities/{activity.id}/support-person/", {"brings": "on"})
    assert resp.status_code == 302
    assert activity.memberships.get(user=adult2).brings_support_person is True
    after = c.get(f"/activities/{activity.id}/").content.decode()
    assert "You're bringing a support person" in after


def test_supervisory_guardian_cannot_set_and_is_not_counted(child, place, activity_type, now):
    # A seated supervisory guardian (ADULT, role=GUARDIAN on a child activity) is themselves the
    # child's support person — they must neither be able to set the flag nor be counted by it.
    from apps.accounts.services import link_guardian
    from apps.social.services import NotAMember, add_guardian

    guardian = make_user("f30_guardian")
    link_guardian(guardian, child)
    activity = create_activity(
        child,
        place=place,
        activity_type=activity_type,
        title="Kids meetup",
        starts_at=now + timedelta(days=1),
        guardian_accompanied=True,
    )
    add_guardian(child, activity, guardian)  # seated GUARDIAN-role member
    with pytest.raises(NotAMember):  # not a voting member -> can't set it
        set_support_companion(guardian, activity, True)
    # Even a guardian row carrying the flag is excluded from the organiser's count.
    gm = activity.memberships.get(user=guardian)
    gm.brings_support_person = True
    gm.save(update_fields=["brings_support_person"])
    row = next(r for r in organizer_console(child)["activities"] if r["activity"].id == activity.id)
    assert row["support_companions"] == 0


def test_brings_support_person_not_on_a_public_member_serializer():
    # inv.2: the flag is organiser-only logistics — it must not leak onto the public membership API.
    from apps.social.serializers import MembershipSerializer

    assert "brings_support_person" not in MembershipSerializer().fields
