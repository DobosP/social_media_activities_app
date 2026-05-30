"""Web tests for wave-3: F13 (guardianship legibility + revoke), F31 (notification prefs)."""

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance, is_guardian_of, link_guardian
from apps.notifications.models import Notification
from apps.notifications.services import get_muted_kinds, notify

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    return u  # cohort CHILD


def _client(user):
    c = Client()
    c.force_login(user)
    return c


# --- F13: guardianship legibility + revoke ---------------------------------------------


def test_guardian_panel_shows_caps_and_revoke():
    guardian, ward = _adult("g13a"), _child("w13a")
    link_guardian(guardian, ward)
    body = _client(guardian).get("/wards/").content.decode()
    assert "What this guardianship lets you do" in body
    assert "End guardianship" in body
    assert "I've arrived" in body  # arrival-ping capability shown for a CHILD ward


def test_guardian_can_end_guardianship():
    guardian, ward = _adult("g13b"), _child("w13b")
    link_guardian(guardian, ward)
    resp = _client(guardian).post(f"/wards/{ward.pk}/revoke/")
    assert resp.status_code == 302
    assert is_guardian_of(guardian, ward) is False


def test_non_guardian_cannot_revoke():
    guardian, ward, stranger = _adult("g13c"), _child("w13c"), _adult("s13c")
    link_guardian(guardian, ward)
    resp = _client(stranger).post(f"/wards/{ward.pk}/revoke/")
    assert resp.status_code == 302  # bounced with an error
    assert is_guardian_of(guardian, ward) is True  # link untouched


def test_ward_sees_legibility_only_guardians_page():
    guardian, ward = _adult("g13d"), _child("w13d")
    link_guardian(guardian, ward)
    body = _client(ward).get("/guardianship/").content.decode()
    assert "g13d" in body  # the guardian is named
    assert "What they can see about you" in body
    assert "doesn't have a" in body  # the "no remove button" legibility note
    assert "/revoke/" not in body  # ward side has no revoke action


# --- F31: notification preferences -----------------------------------------------------


def test_preferences_page_lists_only_mutable_kinds():
    body = _client(_adult("np_a")).get("/notifications/preferences/").content.decode()
    assert 'value="event_reminder"' in body  # a mutable kind is offered
    assert 'value="moderation"' not in body  # DSA kinds are never offered
    assert 'value="system"' not in body


def test_saving_preferences_persists_muted_kinds():
    u = _adult("np_b")
    resp = _client(u).post("/notifications/preferences/", {"muted": ["event_reminder"]})
    assert resp.status_code == 302
    assert get_muted_kinds(u) == {"event_reminder"}


def test_notifications_page_shows_why_you_got_this():
    u = _adult("why_b")
    notify(u, Notification.Kind.JOIN_APPROVED, "in!")
    body = _client(u).get("/notifications/").content.decode()
    assert "Why you got this" in body
