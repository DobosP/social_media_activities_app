"""Web-layer tests for wave-2 features F3 (arrival), F20 (RSVP), F9 (logistics)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name):
    user = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="w2-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="w2-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _activity(owner, *, starts_in=timedelta(minutes=5), **kw):
    return create_activity(
        owner,
        place=_place(),
        activity_type=_type(),
        title="Game",
        starts_at=timezone.now() + starts_in,
        **kw,
    )


# --- F9: logistics card ----------------------------------------------------------------


def test_logistics_card_visible_to_member():
    owner = _user("f9owner")
    member = _user("f9member")
    activity = _activity(owner, meeting_point="North gate by the fountain", what_to_bring="Water")
    _member(activity, member)
    body = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert "Meetup logistics" in body
    assert "North gate by the fountain" in body


def test_logistics_hidden_from_same_cohort_non_member():
    owner = _user("f9owner2")
    stranger = _user("f9stranger")  # same (adult) cohort, NOT a member
    activity = _activity(owner, meeting_point="Secret north gate")
    page = _client(stranger).get(f"/activities/{activity.id}/")
    assert page.status_code == 200  # cohort-visible (like description)
    assert "Secret north gate" not in page.content.decode()  # but the card is member-gated


# --- F20: RSVP -------------------------------------------------------------------------


def test_rsvp_via_web_updates_count():
    owner = _user("f20owner")
    member = _user("f20member")
    activity = _activity(owner)
    _member(activity, member)
    c = _client(member)
    resp = c.post(f"/activities/{activity.id}/rsvp/", {"intent": "going"})
    assert resp.status_code == 302
    body = c.get(f"/activities/{activity.id}/").content.decode()
    assert "Coming:" in body
    assert "1</strong> of 2" in body


# --- F3: arrival -----------------------------------------------------------------------


def test_arrival_button_then_confirmation():
    owner = _user("f3owner")
    member = _user("f3member")
    activity = _activity(owner)  # starts in 5 min → inside the arrival window
    _member(activity, member)
    c = _client(member)
    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "I've arrived" in page
    resp = c.post(f"/activities/{activity.id}/arrived/")
    assert resp.status_code == 302
    after = c.get(f"/activities/{activity.id}/").content.decode()
    assert "marked yourself here" in after


def test_arrival_button_hidden_outside_window():
    owner = _user("f3owner2")
    member = _user("f3member2")
    activity = _activity(owner, starts_in=timedelta(days=3))  # far future → window closed
    _member(activity, member)
    page = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert "I've arrived" not in page
