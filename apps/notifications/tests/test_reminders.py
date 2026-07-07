from datetime import timedelta
from io import StringIO

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner, starts_at, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"rem-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"rem-{slug}-bb", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=starts_at
    )


def test_sends_reminder_for_upcoming_activity_and_is_idempotent():
    owner = _adult("o1")
    soon = timezone.now() + timedelta(hours=3)
    _activity(owner, soon, "soon")

    out = StringIO()
    call_command("send_activity_reminders", "--within-hours=24", stdout=out)
    assert (
        Notification.objects.filter(recipient=owner, kind=Notification.Kind.EVENT_REMINDER).count()
        == 1
    )
    # Second run is a no-op (idempotent).
    call_command("send_activity_reminders", "--within-hours=24", stdout=out)
    assert (
        Notification.objects.filter(recipient=owner, kind=Notification.Kind.EVENT_REMINDER).count()
        == 1
    )


def test_skips_activities_outside_window():
    owner = _adult("o2")
    far = timezone.now() + timedelta(days=10)
    _activity(owner, far, "far")
    call_command("send_activity_reminders", "--within-hours=24", stdout=StringIO())
    assert not Notification.objects.filter(kind=Notification.Kind.EVENT_REMINDER).exists()


def test_notifies_all_current_members():
    owner = _adult("o3")
    soon = timezone.now() + timedelta(hours=2)
    activity = _activity(owner, soon, "all")
    other = _adult("o3b")
    activity.memberships.create(
        user=other, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )

    call_command("send_activity_reminders", stdout=StringIO())
    assert Notification.objects.filter(
        recipient=owner, kind=Notification.Kind.EVENT_REMINDER
    ).exists()
    assert Notification.objects.filter(
        recipient=other, kind=Notification.Kind.EVENT_REMINDER
    ).exists()


# --- W2-F8: logistics folded into the reminder body ------------------------------------------


def _activity_with_logistics(owner, slug, **logistics):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"remlog-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"remlog-{slug}-bb", name="Basketball", category=cat)
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Game",
        starts_at=timezone.now() + timedelta(hours=2),
        **logistics,
    )


def _reminder_body_for(owner):
    return Notification.objects.get(recipient=owner, kind=Notification.Kind.EVENT_REMINDER).body


def test_reminder_body_includes_present_logistics():
    owner = _adult("rl1")
    _activity_with_logistics(
        owner, "present", meeting_point="North gate by the fountain", what_to_bring="Water + ball"
    )
    call_command("send_activity_reminders", stdout=StringIO())
    body = _reminder_body_for(owner)
    assert body.startswith("Starts ")
    assert "Meet: North gate by the fountain" in body
    assert "Bring: Water + ball" in body


def test_reminder_body_degrades_to_bare_line_when_all_blank():
    owner = _adult("rl2")
    _activity_with_logistics(owner, "blank")  # no logistics
    call_command("send_activity_reminders", stdout=StringIO())
    body = _reminder_body_for(owner)
    # Exactly today's bare line — no dangling labels.
    assert body.startswith("Starts ")
    assert "\n" not in body
    for label in ("Meet:", "Bring:", "First time:", "Plan B:"):
        assert label not in body


def test_reminder_body_truncates_long_field_and_caps_total():
    owner = _adult("rl3")
    _activity_with_logistics(
        owner,
        "long",
        meeting_point="x" * 500,
        what_to_bring="y" * 500,
        first_time_note="w" * 500,
    )
    call_command("send_activity_reminders", stdout=StringIO())
    body = _reminder_body_for(owner)
    assert len(body) <= 600  # whole-body cap
    assert "…" in body  # a long field was truncated with an ellipsis


def test_non_member_never_receives_the_logistics_reminder():
    owner = _adult("rl4")
    activity = _activity_with_logistics(owner, "nonmember", meeting_point="Secret gate")
    outsider = _adult("rl4_out")
    # A pending (REQUESTED, not yet admitted) user is not a member.
    activity.memberships.create(
        user=outsider, role=Membership.Role.MEMBER, state=Membership.State.REQUESTED
    )
    call_command("send_activity_reminders", stdout=StringIO())
    assert not Notification.objects.filter(
        recipient=outsider, kind=Notification.Kind.EVENT_REMINDER
    ).exists()
