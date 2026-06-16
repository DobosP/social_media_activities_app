"""W3-F3: guardian safe-departure ping — the "heading home" bookend to the arrival ping.

Mirrors the arrival-ping tests, asserting the two ways the departure ping deliberately DIFFERS:
it fans out to the CHILD's active guardian(s) ONLY (never the group), and its window is
END-relative (live near the meetup's end, where the start-relative arrival window is dead).
"""

from datetime import timedelta

import pytest
from django.core.management import call_command
from django.urls import reverse
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand
from apps.accounts.services import link_guardian
from apps.notifications.models import Notification
from apps.safety.models import AuditLog, Block
from apps.social.models import Activity, Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    NotEligible,
    arrival_window_open,
    create_activity,
    departure_window_open,
    leave_activity,
    mark_departing,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _child_activity(owner, place, activity_type, starts_at, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="After-school chess",
        starts_at=starts_at,
        **kw,
    )


def _member(activity, user, role=Membership.Role.MEMBER):
    return activity.memberships.create(user=user, role=role, state=Membership.State.MEMBER)


def _arrivals_to(user):
    return Notification.objects.filter(recipient=user, kind=Notification.Kind.ARRIVAL)


def _set_window(activity, *, starts_at, ends_at=None):
    """Bypass create_activity's validation to put the meetup into a past/long window."""
    Activity.objects.filter(pk=activity.pk).update(starts_at=starts_at, ends_at=ends_at)
    activity.refresh_from_db()
    return activity


def _ward(name):
    return make_user(name, AgeBand.UNDER_16, consented=True)


def test_mark_departing_notifies_only_guardian(child, place, activity_type, now):
    guardian = make_user("g_dep1")
    link_guardian(guardian, child)
    other = _ward("child_dep2")
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _member(activity, other)
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))

    m = mark_departing(child, activity)
    assert m.departing_at is not None
    assert _arrivals_to(guardian).count() == 1  # only the guardian is told
    assert (
        _arrivals_to(other).count() == 0
    )  # NOT the other member (departure isn't group logistics)
    assert _arrivals_to(child).count() == 0  # NOT the departer themselves
    assert AuditLog.objects.filter(event="activity.departing").exists()


def test_mark_departing_is_idempotent(child, place, activity_type, now):
    guardian = make_user("g_dep_idem")
    link_guardian(guardian, child)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))

    mark_departing(child, activity)
    mark_departing(child, activity)  # a second tap never re-pings
    assert _arrivals_to(guardian).count() == 1


def test_mark_departing_links_to_wards_not_thread(child, place, activity_type, now):
    guardian = make_user("g_dep_url")
    link_guardian(guardian, child)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))

    mark_departing(child, activity)
    note = _arrivals_to(guardian).get()
    # The guardian is cross-cohort to a CHILD thread, so the link goes to /wards/, not the thread.
    assert note.url == reverse("wards")
    # The only departer-derived string is the display name; there is no free-text channel.
    assert child.display_name in note.body
    assert activity.title in note.body


def test_mark_departing_requires_child_cohort(adult, place, activity_type, now):
    activity = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Run",
        starts_at=now + timedelta(hours=1),
    )
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))
    # An adult activity has no supervisory-guardian fan-out; the ping is a child/guardian bookend.
    with pytest.raises(InvalidState):
        mark_departing(adult, activity)


def test_mark_departing_requires_membership(child, place, activity_type, now):
    outsider = _ward("child_out")
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))
    with pytest.raises(NotAMember):
        mark_departing(outsider, activity)


def test_mark_departing_excludes_blocked_guardian(child, place, activity_type, now):
    guardian = make_user("g_dep_blk")
    link_guardian(guardian, child)
    # A block between the pair (defensive): blocked_user_ids excludes the guardian from the fan-out.
    Block.objects.create(blocker=child, blocked=guardian)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))

    mark_departing(child, activity)
    assert _arrivals_to(guardian).count() == 0


def test_departure_window_is_end_relative(child, place, activity_type, now):
    # A long meetup that started 5h ago (arrival window long closed) but ends in 1h: the
    # departure button must still be live — that's the whole reshape.
    guardian = make_user("g_dep_win")
    link_guardian(guardian, child)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(hours=5), ends_at=now + timedelta(hours=1))

    assert arrival_window_open(activity) is False
    assert departure_window_open(activity) is True
    m = mark_departing(child, activity)  # succeeds where the arrival ping would be refused
    assert m.departing_at is not None


def test_departure_window_closed_before_start(child, place, activity_type, now):
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=2))
    assert departure_window_open(activity) is False
    with pytest.raises(InvalidState):
        mark_departing(child, activity)


def test_leave_resets_departing_at(child, place, activity_type, now):
    other = _ward("child_leave")
    link_guardian(make_user("g_other"), other)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _member(activity, other)
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))

    mark_departing(other, activity)
    assert activity.memberships.get(user=other).departing_at is not None
    left = leave_activity(other, activity)  # `other` is a non-owner member, so leaving is allowed
    assert left.departing_at is None


def test_expire_arrivals_clears_departing_end_relative(child, place, activity_type, now):
    # A long meetup: started 8h ago, ended 1h ago (retention window = 6h).
    #  - arrived_at  (start-relative) IS cleared: start 8h ago < cutoff 6h ago
    #  - departing_at (end-relative) is NOT cleared: end 1h ago is within the retention window
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(hours=8), ends_at=now - timedelta(hours=1))
    m = activity.memberships.get(user=child)
    m.arrived_at = now - timedelta(hours=7)
    m.departing_at = now - timedelta(hours=1)
    m.save(update_fields=["arrived_at", "departing_at"])

    call_command("expire_arrivals")  # default retention 6h
    m.refresh_from_db()
    assert m.arrived_at is None  # start-relative cue cleared
    assert m.departing_at is not None  # end-relative ping retained (meetup ended only 1h ago)

    # Push the end past the retention window → the departure ping now clears too.
    Activity.objects.filter(pk=activity.pk).update(ends_at=now - timedelta(hours=7))
    call_command("expire_arrivals")
    m.refresh_from_db()
    assert m.departing_at is None


def test_departing_action_via_api(child, place, activity_type, now):
    guardian = make_user("g_dep_api")
    link_guardian(guardian, child)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))

    client = APIClient()
    client.force_authenticate(child)
    resp = client.post(f"/api/social/activities/{activity.id}/departing/")
    assert resp.status_code == 200, resp.content
    assert resp.json()["departing_at"] is not None
    assert _arrivals_to(guardian).count() == 1


def test_mark_departing_requires_can_participate(child, place, activity_type, now):
    # A child without active parental consent fails can_participate before any guardian fan-out.
    no_consent = make_user("child_noconsent", AgeBand.UNDER_16, consented=False)
    guardian = make_user("g_dep_nc")
    link_guardian(guardian, no_consent)
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _member(activity, no_consent)
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))
    with pytest.raises(NotEligible):
        mark_departing(no_consent, activity)
    assert _arrivals_to(guardian).count() == 0


def test_mark_departing_refused_when_completed(child, place, activity_type, now):
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(minutes=30), ends_at=now + timedelta(hours=1))
    Activity.objects.filter(pk=activity.pk).update(status=Activity.Status.COMPLETED)
    activity.refresh_from_db()
    assert departure_window_open(activity) is False  # window is OPEN-only
    with pytest.raises(InvalidState):
        mark_departing(child, activity)


def test_departure_window_closed_after_end_plus_window(child, place, activity_type, now):
    # Ended 5h ago; the window closes DEPARTURE_WINDOW_AFTER_HOURS (3h) after the end → closed now.
    activity = _child_activity(child, place, activity_type, now + timedelta(hours=1))
    _set_window(activity, starts_at=now - timedelta(hours=8), ends_at=now - timedelta(hours=5))
    assert departure_window_open(activity) is False
    with pytest.raises(InvalidState):
        mark_departing(child, activity)
