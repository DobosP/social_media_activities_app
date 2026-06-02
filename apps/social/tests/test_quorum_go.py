"""F1 Quorum-go: an owner-set minimum-GOING threshold that, once first reached, fires a single
"it's on" notice. Pins: the LIVE derived chip state (never the one-shot latch), the one-shot
notification (no spam when the count wobbles), blocked-pair exclusion, and the edit path."""

from datetime import timedelta

import pytest

from apps.notifications.models import MUTABLE_KINDS, NON_MUTABLE_KINDS, Notification
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Membership

from .conftest import make_user

pytestmark = pytest.mark.django_db

GOING = Membership.AttendanceIntent.GOING
NOT_GOING = Membership.AttendanceIntent.NOT_GOING
CONFIRMED = Notification.Kind.MEETUP_CONFIRMED


def _activity(owner, place, activity_type, now, *, starts_at=None, **kw):
    return social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=starts_at or now,
        **kw,
    )


def _join(activity, user):
    return Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _confirmed_recipients():
    return set(Notification.objects.filter(kind=CONFIRMED).values_list("recipient_id", flat=True))


def test_attendance_summary_quorum_state_is_derived_live(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, min_to_go=2)
    _join(a, make_user("q_sum_m"))
    s = social.attendance_summary(a)  # nobody GOING yet
    assert s["min_to_go"] == 2
    assert s["met_minimum"] is False
    assert s["remaining_needed"] == 2


def test_no_threshold_means_no_quorum_state_or_notice(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now)  # min_to_go unset
    m = make_user("q_none_m")
    _join(a, m)
    social.set_attendance_intent(adult, a, GOING)
    social.set_attendance_intent(m, a, GOING)
    s = social.attendance_summary(a)
    assert s["min_to_go"] is None and s["met_minimum"] is None and s["remaining_needed"] is None
    assert not Notification.objects.filter(kind=CONFIRMED).exists()


def test_crossing_min_latches_and_notifies_current_members_once(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, min_to_go=2)
    m1 = make_user("q_x_m1")
    _join(a, m1)
    social.set_attendance_intent(adult, a, GOING)  # going=1, below
    a.refresh_from_db()
    assert a.go_confirmed_at is None and not Notification.objects.filter(kind=CONFIRMED).exists()

    social.set_attendance_intent(m1, a, GOING)  # going=2, crosses
    a.refresh_from_db()
    assert a.go_confirmed_at is not None
    assert _confirmed_recipients() == {adult.id, m1.id}  # every current member, once


def test_notice_is_one_shot_even_when_count_wobbles(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, min_to_go=2)
    m1 = make_user("q_w_m1")
    _join(a, m1)
    social.set_attendance_intent(adult, a, GOING)
    social.set_attendance_intent(m1, a, GOING)  # confirmed
    before = Notification.objects.filter(kind=CONFIRMED).count()
    assert before == 2

    # A member drops out: the live chip is honest again, but the latch persists (no re-notify).
    social.set_attendance_intent(m1, a, NOT_GOING)
    s = social.attendance_summary(a)
    assert s["met_minimum"] is False and s["remaining_needed"] == 1
    a.refresh_from_db()
    assert a.go_confirmed_at is not None

    # Re-crossing does NOT fire a second notice (one-shot).
    social.set_attendance_intent(m1, a, GOING)
    assert Notification.objects.filter(kind=CONFIRMED).count() == before
    # ...and a member who joined AFTER the confirmation isn't retro-notified.
    late = make_user("q_w_late")
    _join(a, late)
    social.set_attendance_intent(late, a, GOING)
    assert late.id not in _confirmed_recipients()


def test_confirm_fanout_excludes_blocked_pairs(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, min_to_go=2)
    blocked = make_user("q_blocked")
    _join(a, blocked)
    block_user(adult, blocked)  # owner <-> member block
    social.set_attendance_intent(adult, a, GOING)
    social.set_attendance_intent(blocked, a, GOING)  # their GOING still counts toward quorum...
    a.refresh_from_db()
    assert a.go_confirmed_at is not None  # ...so the meetup confirms
    recips = _confirmed_recipients()
    assert adult.id in recips and blocked.id not in recips  # ...but they're not notified


def test_min_to_go_is_create_settable_and_editable(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, starts_at=now + timedelta(days=1), min_to_go=5)
    assert a.min_to_go == 5
    assert "min_to_go" in social.ACTIVITY_EDITABLE_FIELDS
    social.update_activity(adult, a, min_to_go=3)
    a.refresh_from_db()
    assert a.min_to_go == 3
    social.update_activity(adult, a, min_to_go=None)  # clearing the threshold is allowed
    a.refresh_from_db()
    assert a.min_to_go is None


def test_meetup_confirmed_kind_is_mutable():
    # A confirmation notice is convenience, not a DSA safety notice — it must be user-mutable.
    assert CONFIRMED in MUTABLE_KINDS
    assert CONFIRMED not in NON_MUTABLE_KINDS


def test_quorum_chip_renders_live_state_in_web_detail(adult, place, activity_type, now):
    # Exercises the new RSVP-panel chip block (existing web tests never set min_to_go).
    from django.test import Client

    a = _activity(adult, place, activity_type, now, starts_at=now + timedelta(days=1), min_to_go=3)
    client = Client()
    client.force_login(adult)
    html = client.get(f"/activities/{a.pk}/").content.decode()
    assert "needs 3 more to happen" in html  # owner is a member, nobody GOING yet
    social.set_attendance_intent(adult, a, GOING)
    social.update_activity(adult, a, min_to_go=1)  # now met (1 going >= 1)
    html2 = client.get(f"/activities/{a.pk}/").content.decode()
    assert "It's on" in html2


# --- review-remediation regressions (no lying chip / no false notice on a non-OPEN meetup) ------


def test_cancel_yields_no_lying_chip_and_no_confirm(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, min_to_go=2)
    m1 = make_user("q_cancel_m1")
    _join(a, m1)
    social.set_attendance_intent(adult, a, GOING)  # going=1
    social.cancel_activity(adult, a)
    a.refresh_from_db()
    n_before = Notification.objects.filter(kind=CONFIRMED).count()
    # RSVPing on a frozen (cancelled) meetup must NOT confirm or fire an "it's happening" notice...
    social.set_attendance_intent(m1, a, GOING)  # would cross 2 if it were open
    a.refresh_from_db()
    assert a.go_confirmed_at is None
    assert Notification.objects.filter(kind=CONFIRMED).count() == n_before
    # ...and the live summary carries NO quorum state, so the chip can't lie "It's on".
    s = social.attendance_summary(a)
    assert s["min_to_go"] is None and s["met_minimum"] is None and s["remaining_needed"] is None


def test_lowering_min_below_live_count_fires_the_one_shot(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now, starts_at=now + timedelta(days=1), min_to_go=5)
    m1 = make_user("q_lower_m1")
    _join(a, m1)
    social.set_attendance_intent(adult, a, GOING)
    social.set_attendance_intent(m1, a, GOING)  # going=2, below 5 -> no confirm yet
    assert not Notification.objects.filter(kind=CONFIRMED).exists()
    social.update_activity(adult, a, min_to_go=2)  # lower to 2 -> met on the LIVE count
    a.refresh_from_db()
    assert a.go_confirmed_at is not None
    assert _confirmed_recipients() == {adult.id, m1.id}


def test_min_to_go_cannot_exceed_capacity(adult, place, activity_type, now):
    with pytest.raises(social.InvalidState):
        _activity(adult, place, activity_type, now, capacity=3, min_to_go=5)
    a = _activity(
        adult, place, activity_type, now, starts_at=now + timedelta(days=1), capacity=5, min_to_go=2
    )
    with pytest.raises(social.InvalidState):
        social.update_activity(adult, a, min_to_go=10)  # > capacity (5)
