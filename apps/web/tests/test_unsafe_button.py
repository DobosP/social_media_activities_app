"""F8 — one-tap "I feel unsafe" button (web layer). Pins: the button POSTs as a member (not owner)
and files a report; non-members and the owner can't use the endpoint; the safe-exit card renders
the one-tap button; and (for a CHILD member) the active guardian is alerted via the service."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.safety.models import ReasonCode, Report
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="uw-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="uw-ball", defaults={"name": "Ball", "category": cat}
    )
    return t


def _place():
    return Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _activity(owner):
    return create_activity(
        owner, place=_place(), activity_type=_type(), title="Game", starts_at="2031-06-01T10:00Z"
    )


def _add_member(activity, user):
    """Make `user` an approved MEMBER of `activity` directly (bypass the join gate for setup)."""
    return Membership.objects.create(
        activity=activity,
        user=user,
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )


def test_member_can_file_unsafe_and_card_shows_button():
    owner = _user("uw_owner")
    activity = _activity(owner)
    member = _user("uw_member")
    _add_member(activity, member)

    page = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert "/unsafe/" in page  # the one-tap button posts to the unsafe endpoint
    assert "I feel unsafe" in page

    resp = _client(member).post(f"/activities/{activity.id}/unsafe/")
    assert resp.status_code == 302
    assert Report.objects.filter(
        reporter=member, target_id=activity.id, reason=ReasonCode.OFF_PLATFORM
    ).exists()


def test_owner_cannot_use_the_endpoint():
    owner = _user("uw_owner2")
    activity = _activity(owner)
    before = Report.objects.count()
    resp = _client(owner).post(f"/activities/{activity.id}/unsafe/")
    assert resp.status_code == 302
    assert Report.objects.count() == before  # owner is excluded (mirrors the card gate)


def test_non_member_cannot_use_the_endpoint():
    owner = _user("uw_owner3")
    activity = _activity(owner)
    outsider = _user("uw_outsider")  # same cohort (can see it) but NOT a member
    before = Report.objects.count()
    resp = _client(outsider).post(f"/activities/{activity.id}/unsafe/")
    assert resp.status_code == 302
    assert Report.objects.count() == before  # not a member -> no drive-by report


def test_get_is_not_allowed():
    owner = _user("uw_owner4")
    activity = _activity(owner)
    member = _user("uw_member4")
    _add_member(activity, member)
    resp = _client(member).get(f"/activities/{activity.id}/unsafe/")
    assert resp.status_code == 405  # POST-only


def test_child_member_alerts_active_guardian_via_web():
    owner = _user("uw_childowner", AgeBand.UNDER_16, consented=True)  # CHILD-cohort activity
    activity = _activity(owner)
    kid = _user("uw_kid", AgeBand.UNDER_16, consented=True)
    _add_member(activity, kid)
    guardian = _user("uw_parent")
    link_guardian(guardian, kid)

    resp = _client(kid).post(f"/activities/{activity.id}/unsafe/", follow=True)
    assert resp.status_code == 200
    assert (
        Notification.objects.filter(recipient=guardian, kind=Notification.Kind.SYSTEM).count() == 1
    )
    assert "grown-ups who look after you have been told" in resp.content.decode()


def test_teen_member_is_not_told_a_guardian_was_alerted():
    """Finding 4: a TEEN triggers no guardian fan-out, so the message must not promise one."""
    owner = _user("uw_teenowner", AgeBand.AGE_16_17, consented=True)  # TEEN-cohort activity
    activity = _activity(owner)
    teen = _user("uw_teen", AgeBand.AGE_16_17, consented=True)
    _add_member(activity, teen)
    guardian = _user("uw_teenparent")
    link_guardian(guardian, teen)

    resp = _client(teen).post(f"/activities/{activity.id}/unsafe/", follow=True)
    assert resp.status_code == 200
    assert (
        Notification.objects.filter(recipient=guardian, kind=Notification.Kind.SYSTEM).count() == 0
    )
    body = resp.content.decode()
    assert "grown-ups who look after you" not in body  # no false guardian promise to a teen
    assert "A moderator has been alerted" in body


def test_guardian_role_member_cannot_use_endpoint():
    """Finding 3: a supervisory GUARDIAN-seat membership is excluded from the member fast path.
    Uses a same-cohort setup so the GUARDIAN-role check is reached (not masked by a cohort 404)."""
    owner = _user("uw_gowner")  # ADULT-cohort activity
    activity = _activity(owner)
    seat = _user("uw_gseat")  # same cohort -> can see the activity
    Membership.objects.create(
        activity=activity,
        user=seat,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )
    before = Report.objects.count()
    resp = _client(seat).post(f"/activities/{activity.id}/unsafe/")
    assert resp.status_code == 302
    assert Report.objects.count() == before  # GUARDIAN seat is excluded from the fast path
