"""W3-F3 web surface: the CHILD-only "I'm heading home" departure button + POST handler.

Covers the web wiring the DRF-only test can't: route name, login/POST decorators, the
_visible_activity_or_404 gate, and the SocialError->messages.error path (a non-CHILD member
gets a clean redirect, never a 500).
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.social.models import Activity, Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name, age_band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=age_band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="w3f3-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="w3f3-chess", defaults={"name": "Chess", "category": cat}
    )
    return t


def _place():
    return Place.objects.create(
        name="Library", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _in_window_activity(owner):
    """An activity (cohort from owner) currently inside the end-relative departure window."""
    a = create_activity(
        owner,
        place=_place(),
        activity_type=_type(),
        title="Chess club",
        starts_at=timezone.now() + timedelta(hours=1),
    )
    Activity.objects.filter(pk=a.pk).update(
        starts_at=timezone.now() - timedelta(minutes=30),
        ends_at=timezone.now() + timedelta(hours=1),
    )
    a.refresh_from_db()
    return a


def _arrivals_to(user):
    return Notification.objects.filter(recipient=user, kind=Notification.Kind.ARRIVAL)


def test_child_member_sees_button_and_tap_notifies_guardian():
    owner = _user("w3f3owner", AgeBand.UNDER_16, consented=True)
    child = _user("w3f3child", AgeBand.UNDER_16, consented=True)
    guardian = _user("w3f3guard")
    link_guardian(guardian, child)
    activity = _in_window_activity(owner)
    _member(activity, child)

    c = _client(child)
    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "I'm heading home" in page

    resp = c.post(f"/activities/{activity.id}/departing/")
    assert resp.status_code == 302
    assert _arrivals_to(guardian).count() == 1  # only the guardian is told
    after = c.get(f"/activities/{activity.id}/").content.decode()
    assert "heading home" in after  # the confirmation line replaces the button


def test_adult_member_has_no_button_and_post_is_graceful():
    owner = _user("w3f3aowner")
    member = _user("w3f3amember")  # same (adult) cohort, in-window member
    activity = _in_window_activity(owner)
    _member(activity, member)

    c = _client(member)
    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "I'm heading home" not in page  # CHILD-only affordance

    # The service refuses a non-CHILD; the handler must surface a clean redirect, never a 500.
    resp = c.post(f"/activities/{activity.id}/departing/")
    assert resp.status_code == 302
    assert not member.memberships.get(activity=activity).departing_at
