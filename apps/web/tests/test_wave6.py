"""Web tests for wave-6: F35 (catch-up gate), F39 (welcome banner TTL), F36 (draft prefill)."""

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
from apps.social.services import create_activity, post_to_thread
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type(name="Basketball", slug="w6-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="w6w-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(slug=slug, defaults={"name": name, "category": cat})
    return t


def _place(name="Central Park"):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner):
    return create_activity(
        owner, place=_place(), activity_type=_type(), title="Game", starts_at=timezone.now()
    )


# --- F35: catch-up gate ----------------------------------------------------------------


def test_catch_up_visible_to_member():
    owner = _user("f35o")
    a = _activity(owner)
    post_to_thread(owner, a, "Meeting point moved to the north gate.")
    body = _client(owner).get(f"/activities/{a.id}/").content.decode()
    assert "Catch up" in body
    assert "north gate" in body


def test_catch_up_hidden_from_non_member():
    owner, stranger = _user("f35o2"), _user("f35s")  # same cohort, not a member
    a = _activity(owner)
    post_to_thread(owner, a, "Secret logistics: meet at the back.")
    body = _client(stranger).get(f"/activities/{a.id}/").content.decode()
    assert "Catch up" not in body
    assert "Secret logistics" not in body  # thread content stays member-gated


# --- F39: welcome banner ---------------------------------------------------------------


def test_welcome_banner_for_first_timer():
    owner, viewer = _user("f39o"), _user("f39v")
    a = _activity(owner)
    a.memberships.create(
        user=viewer,
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
        welcomed_at=timezone.now(),
    )
    body = _client(viewer).get(f"/activities/{a.id}/").content.decode()
    assert "Welcome to your first meetup" in body


def test_welcome_banner_expires_after_ttl():
    owner, viewer = _user("f39o2"), _user("f39v2")
    a = _activity(owner)
    a.memberships.create(
        user=viewer,
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
        welcomed_at=timezone.now() - timedelta(days=30),
    )
    body = _client(viewer).get(f"/activities/{a.id}/").content.decode()
    assert "Welcome to your first meetup" not in body


# --- F36: draft prefill ----------------------------------------------------------------


def test_create_form_prefilled_with_draft():
    user = _user("f36u")
    atype = _type()
    place = _place(name="Central Park")
    resp = _client(user).get(
        f"/activities/new/?place={place.id}&activity_type={atype.id}&starts_at=2031-06-15T12:30"
    )
    assert resp.status_code == 200
    initial = resp.context["form"].initial
    assert initial.get("title") == "Basketball at Central Park"
    assert "A Basketball meetup at Central Park" in initial.get("description", "")


def test_no_draft_without_activity_type():
    user = _user("f36n")
    place = _place()
    resp = _client(user).get(f"/activities/new/?place={place.id}")
    assert resp.status_code == 200
    assert "title" not in resp.context["form"].initial  # no type → no draft
