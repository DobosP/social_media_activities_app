"""Web tests for wave-4: F22 (did-we-meet), F14 (age provenance), F19 (safety record)."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import ModerationAction, ReasonCode
from apps.safety.services import file_report, take_action
from apps.social.models import Membership
from apps.social.services import complete_activity, create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="w4-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="w4-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


# --- F22: did we meet? -----------------------------------------------------------------


def test_met_card_appears_only_when_completed():
    owner, member = _user("f22o"), _user("f22m")
    activity = _activity(owner)
    _member(activity, member)
    c = _client(member)
    assert "Did this meet up?" not in c.get(f"/activities/{activity.id}/").content.decode()
    complete_activity(activity)
    body = c.get(f"/activities/{activity.id}/").content.decode()
    assert "Did this meet up?" in body
    resp = c.post(f"/activities/{activity.id}/met/")
    assert resp.status_code == 302
    after = c.get(f"/activities/{activity.id}/").content.decode()
    assert "you confirmed" in after
    assert "Confirmed: <strong>1</strong> of 2" in after


# --- F14: age provenance panel ---------------------------------------------------------


def test_profile_shows_provenance_without_pii():
    body = _client(_user("f14u")).get("/profile/").content.decode()
    assert "Verified as:" in body
    assert "Re-verify" in body  # has a proof on file → re-verify, not first-time verify
    # No raw attestation internals leak onto the page.
    for marker in ("age_over_16", "age_over_18", "holder_proof", "jwt_vc"):
        assert marker not in body


# --- F19: your safety record -----------------------------------------------------------


def test_safety_record_shows_own_records_only():
    user, other, mod = _user("f19u"), _user("f19o"), _user("f19mod")
    take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM, notes="secret note")
    take_action(mod, other, ModerationAction.Action.WARN, ReasonCode.SPAM)  # not the viewer's
    file_report(user, other, ReasonCode.HARASSMENT, "they were rude")
    body = _client(user).get("/my-safety-record/").content.decode()
    assert "Your safety record" in body
    assert "your account" in body  # the warn on the viewer's account
    assert "they were rude" in body  # the viewer's own report detail
    # Never leak the moderator identity or their private notes.
    assert "f19mod" not in body
    assert "secret note" not in body


def test_safety_record_is_login_gated():
    resp = Client().get("/my-safety-record/")
    assert resp.status_code in (302, 301)  # redirect to login
