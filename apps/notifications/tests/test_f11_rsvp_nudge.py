"""W2-F11: one quiet 'still coming?' RSVP nudge to undecided members inside the arrival window.

At-most-once per (member, activity), mutable (F31), excludes guardians and members who already
RSVP'd, deep-links to the WEB detail page. No shaming, no per-user reliability rollup.
"""

from datetime import timedelta
from io import StringIO

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.notifications.models import Notification
from apps.notifications.services import set_muted_kinds
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import add_guardian, create_activity, set_attendance_intent
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
K = Notification.Kind


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if band == AgeBand.UNDER_16:
        from apps.accounts.models import ParentalConsent

        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _activity(owner, slug, *, hours=1, **kw):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat, _ = ActivityCategory.objects.get_or_create(slug=f"n-{slug}", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug=f"n-{slug}-bb", defaults={"name": "Basketball", "category": cat}
    )
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Game",
        starts_at=timezone.now() + timedelta(hours=hours),
        **kw,
    )


def _member(activity, user, role=Membership.Role.MEMBER):
    return activity.memberships.create(user=user, role=role, state=Membership.State.MEMBER)


def _nudges(user):
    return Notification.objects.filter(recipient=user, kind=K.RSVP_NUDGE)


def _run():
    call_command("rsvp_finalize_nudge", stdout=StringIO())


def test_nudges_undecided_member_in_window_and_is_idempotent():
    owner = _user("o")
    member = _user("m")
    activity = _activity(owner, "a")  # starts in 1h -> inside the arrival window
    _member(activity, member)
    _run()
    assert _nudges(member).count() == 1
    _run()  # second tick -> no re-nudge
    assert _nudges(member).count() == 1


def test_member_who_rsvped_is_not_nudged():
    owner = _user("o2")
    going = _user("going")
    not_going = _user("nope")
    activity = _activity(owner, "b")
    _member(activity, going)
    _member(activity, not_going)
    set_attendance_intent(going, activity, Membership.AttendanceIntent.GOING)
    set_attendance_intent(not_going, activity, Membership.AttendanceIntent.NOT_GOING)
    _run()
    assert _nudges(going).count() == 0
    assert _nudges(not_going).count() == 0


def test_guardian_is_not_nudged():
    child = _user("child", AgeBand.UNDER_16)
    guardian = _user("guard")
    link_guardian(guardian, child)
    activity = _activity(child, "c", guardian_accompanied=True)  # CHILD cohort
    add_guardian(child, activity, guardian)  # seated GUARDIAN-role member
    _run()
    assert _nudges(guardian).count() == 0  # voting_members excludes guardians


def test_no_nudge_outside_the_arrival_window():
    # 2.5h out PASSES the queryset pre-filter (start <= now + before+1h) but FAILS the authoritative
    # arrival_window_open gate (window opens only at start - 2h) — so this exercises the GATE, not
    # just the pre-filter. 2.5h is genuinely too early for the window.
    owner = _user("o3")
    member = _user("m3")
    activity = _activity(owner, "d", hours=2.5)
    _member(activity, member)
    _run()
    assert _nudges(member).count() == 0

    far = _activity(_user("o3b"), "d2", hours=48)  # far future -> excluded by the pre-filter
    _member(far, _user("m3b"))
    _run()
    assert _nudges(member).count() == 0


def test_no_nudge_for_a_cancelled_activity():
    from apps.social.services import cancel_activity

    owner = _user("o6")
    member = _user("m6")
    activity = _activity(owner, "g")  # in-window
    _member(activity, member)
    cancel_activity(owner, activity)  # status != OPEN
    _run()
    assert _nudges(member).count() == 0


def test_muted_member_gets_nothing():
    owner = _user("o4")
    member = _user("m4")
    activity = _activity(owner, "e")
    _member(activity, member)
    set_muted_kinds(member, [K.RSVP_NUDGE])  # F31: mutable kind, opted out
    _run()
    assert _nudges(member).count() == 0


def test_nudge_deep_links_to_web_detail_not_api():
    owner = _user("o5")
    member = _user("m5")
    activity = _activity(owner, "f")
    _member(activity, member)
    _run()
    note = _nudges(member).get()
    assert note.url == f"/activities/{activity.id}/"  # web page, not /api/...
    assert "/api/" not in note.url


def test_registered_in_due_jobs():
    from apps.ops.management.commands import run_due_jobs

    assert "rsvp_finalize_nudge" in {name for name, _ in run_due_jobs.DUE_JOBS}
