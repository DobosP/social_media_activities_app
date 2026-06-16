"""W4-F11: a calm inline prompt to the organiser when a meetup hits its go-quorum (it's actually
happening) but still has no meeting point — no notification, no job; self-suppresses once set."""

from datetime import timedelta

import pytest
from django.test import Client

from apps.social.models import Membership
from apps.social.services import (
    attendance_summary,
    create_activity,
    quorum_locked_without_meeting_point,
    set_attendance_intent,
)

pytestmark = pytest.mark.django_db
BANNER = "going ahead"  # substring of the W4-F11 banner copy


def _quorum_meetup(owner, place, activity_type, now, *, meeting_point=""):
    a = create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=now + timedelta(days=1),
        min_to_go=1,
        meeting_point=meeting_point,
    )
    set_attendance_intent(owner, a, "going")  # owner GOING -> going=1 >= min_to_go=1 (quorum met)
    return a


def test_helper_true_when_quorum_met_and_no_meeting_point(adult, place, activity_type, now):
    a = _quorum_meetup(adult, place, activity_type, now)
    assert quorum_locked_without_meeting_point(a, attendance_summary(a)) is True


def test_helper_false_when_meeting_point_set(adult, place, activity_type, now):
    a = _quorum_meetup(adult, place, activity_type, now, meeting_point="North gate")
    assert quorum_locked_without_meeting_point(a, attendance_summary(a)) is False


def test_helper_false_when_quorum_not_met(adult, place, activity_type, now):
    a = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=now + timedelta(days=1),
        min_to_go=5,  # owner alone (going=1) is below the minimum
    )
    assert quorum_locked_without_meeting_point(a, attendance_summary(a)) is False


def test_helper_false_when_no_min_to_go(adult, place, activity_type, now):
    a = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=now + timedelta(days=1),  # no quorum configured -> met_minimum None
    )
    assert quorum_locked_without_meeting_point(a, attendance_summary(a)) is False


def test_organizer_sees_the_banner(adult, place, activity_type, now):
    a = _quorum_meetup(adult, place, activity_type, now)
    c = Client()
    c.force_login(adult)
    assert BANNER in c.get(f"/activities/{a.id}/").content.decode()


def test_banner_absent_once_meeting_point_set(adult, place, activity_type, now):
    a = _quorum_meetup(adult, place, activity_type, now, meeting_point="North gate by the fountain")
    c = Client()
    c.force_login(adult)
    assert BANNER not in c.get(f"/activities/{a.id}/").content.decode()


def test_non_organizer_member_does_not_see_the_banner(adult, adult2, place, activity_type, now):
    a = _quorum_meetup(adult, place, activity_type, now)
    a.memberships.create(user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER)
    c = Client()
    c.force_login(adult2)
    assert BANNER not in c.get(f"/activities/{a.id}/").content.decode()  # organiser-only prompt
