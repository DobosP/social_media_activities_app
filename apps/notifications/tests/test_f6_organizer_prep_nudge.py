"""W3-F6: one calm, muteable prep-gap nudge to a meetup's organisers when it starts soon
with no meeting point. At-most-once per (organiser, activity), mutable (F31), owner +
co-organisers only (no member fan-out), STABLE dedup url (no timestamp/window).
"""

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
from apps.notifications.services import set_muted_kinds
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import cancel_activity, create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
K = Notification.Kind


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
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


def _coorg(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.CO_ORGANIZER, state=Membership.State.MEMBER
    )


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _prep(user):
    return Notification.objects.filter(recipient=user, kind=K.ORGANIZER_PREP)


def _run():
    call_command("organizer_prep_nudge", stdout=StringIO())


def test_nudges_owner_when_meeting_point_blank_and_is_idempotent():
    owner = _user("o")
    _activity(owner, "a")  # starts in 1h, blank meeting_point
    _run()
    assert _prep(owner).count() == 1
    _run()  # second tick -> stable-url dedup, no re-nudge
    assert _prep(owner).count() == 1


def test_nudges_owner_and_coorganizer_not_regular_member():
    owner = _user("o2")
    coorg = _user("co2")
    member = _user("m2")
    activity = _activity(owner, "b")
    _coorg(activity, coorg)
    _member(activity, member)
    _run()
    assert _prep(owner).count() == 1
    assert _prep(coorg).count() == 1
    assert _prep(member).count() == 0  # regular members are never nudged


def test_no_nudge_when_meeting_point_set():
    owner = _user("o3")
    activity = _activity(owner, "c")
    activity.meeting_point = "Main gate, by the noticeboard"
    activity.save(update_fields=["meeting_point"])
    _run()
    assert _prep(owner).count() == 0


def test_whitespace_only_meeting_point_counts_as_blank():
    owner = _user("o3b")
    activity = _activity(owner, "cb")
    activity.meeting_point = "   "
    activity.save(update_fields=["meeting_point"])
    _run()
    assert _prep(owner).count() == 1


def test_no_nudge_outside_prep_window():
    owner = _user("o4")
    _activity(owner, "d", hours=72)  # 72h out -> beyond the 48h prep window
    _run()
    assert _prep(owner).count() == 0


def test_no_nudge_for_cancelled_activity():
    owner = _user("o5")
    activity = _activity(owner, "e")
    cancel_activity(owner, activity)  # status != OPEN
    _run()
    assert _prep(owner).count() == 0


def test_muted_organizer_gets_nothing():
    owner = _user("o6")
    _activity(owner, "f")
    set_muted_kinds(owner, [K.ORGANIZER_PREP])  # F31: mutable kind, opted out
    _run()
    assert _prep(owner).count() == 0


def test_nudge_deep_links_to_web_detail_not_api():
    owner = _user("o7")
    activity = _activity(owner, "g")
    _run()
    note = _prep(owner).get()
    assert note.url == f"/activities/{activity.id}/"  # web page, the stable dedup key
    assert "/api/" not in note.url


def test_registered_in_due_jobs():
    from apps.ops.management.commands import run_due_jobs

    assert "organizer_prep_nudge" in {name for name, _ in run_due_jobs.DUE_JOBS}
