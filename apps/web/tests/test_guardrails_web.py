"""Web tests for F7 — guardian-set participation guardrails (edit on /wards/, legibility on
/guardianship/, and the gate-respecting authorisation)."""

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, guardrail_for, link_guardian

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _teen(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def test_guardrail_form_shown_for_child_ward():
    guardian, ward = _adult("gf1"), _child("wf1")
    link_guardian(guardian, ward)
    body = _client(guardian).get("/wards/").content.decode()
    assert "Set participation limits" in body
    assert f"/wards/{ward.pk}/limits/" in body


def test_guardrail_form_hidden_for_teen_ward():
    guardian, teen = _adult("gf2"), _teen("wf2")
    link_guardian(guardian, teen)
    body = _client(guardian).get("/wards/").content.decode()
    assert "Set participation limits" not in body


def test_guardian_saves_guardrail():
    guardian, ward = _adult("gf3"), _child("wf3")
    link_guardian(guardian, ward)
    resp = _client(guardian).post(
        f"/wards/{ward.pk}/limits/",
        {"supervised_only": "on", "latest_start_hour": "18", "max_open_joins": "3"},
    )
    assert resp.status_code == 302
    rail = guardrail_for(guardian, ward)
    assert rail.supervised_only is True
    assert rail.latest_start_hour == 18
    assert rail.max_open_joins == 3


def test_unchecked_supervised_only_clears_it():
    guardian, ward = _adult("gf3b"), _child("wf3b")
    link_guardian(guardian, ward)
    c = _client(guardian)
    c.post(f"/wards/{ward.pk}/limits/", {"supervised_only": "on"})
    assert guardrail_for(guardian, ward).supervised_only is True
    # A second save with the checkbox absent must turn it OFF (HTML omits unchecked boxes).
    c.post(f"/wards/{ward.pk}/limits/", {"latest_start_hour": ""})
    assert guardrail_for(guardian, ward).supervised_only is False


def test_non_guardian_cannot_set_limits():
    guardian, ward, stranger = _adult("gf4"), _child("wf4"), _adult("sf4")
    link_guardian(guardian, ward)
    resp = _client(stranger).post(f"/wards/{ward.pk}/limits/", {"latest_start_hour": "10"})
    assert resp.status_code == 302  # bounced with an error
    assert guardrail_for(stranger, ward) is None
    assert guardrail_for(guardian, ward) is None  # nothing was written


def test_no_enumeration_oracle_for_nonexistent_vs_existing(settings):
    # A non-guardian must get the SAME response (302 redirect, not a 404) whether the pk is a
    # real user or not — no user-enumeration oracle on child accounts.
    guardian, ward, stranger = _adult("gf4b"), _child("wf4b"), _adult("sf4b")
    link_guardian(guardian, ward)
    c = _client(stranger)
    existing = c.post(f"/wards/{ward.pk}/limits/", {"latest_start_hour": "10"})
    missing = c.post(f"/wards/{99999999}/limits/", {"latest_start_hour": "10"})
    assert existing.status_code == missing.status_code == 302
    assert existing.url == missing.url


def test_rate_limited(settings):
    settings.GUARDIAN_GUARDRAIL_RATE_LIMIT = 2
    settings.GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS = 3600
    guardian, ward = _adult("gf4c"), _child("wf4c")
    link_guardian(guardian, ward)
    c = _client(guardian)
    for _ in range(2):
        c.post(f"/wards/{ward.pk}/limits/", {"latest_start_hour": "10"})
    resp = c.post(f"/wards/{ward.pk}/limits/", {"latest_start_hour": "11"}, follow=True)
    assert b"Too many updates" in resp.content
    assert guardrail_for(guardian, ward).latest_start_hour == 10  # the 3rd edit didn't land


def test_invalid_hour_is_rejected_with_message():
    guardian, ward = _adult("gf5"), _child("wf5")
    link_guardian(guardian, ward)
    resp = _client(guardian).post(
        f"/wards/{ward.pk}/limits/", {"latest_start_hour": "99"}, follow=True
    )
    assert guardrail_for(guardian, ward) is None  # not saved
    assert b"between 0 and 23" in resp.content


def test_ward_sees_set_limits_on_guardianship_page():
    guardian, ward = _adult("gf6"), _child("wf6")
    link_guardian(guardian, ward)
    _client(guardian).post(
        f"/wards/{ward.pk}/limits/",
        {"supervised_only": "on", "latest_start_hour": "17"},
    )
    body = _client(ward).get("/guardianship/").content.decode()
    assert "Limits they&#x27;ve set" in body or "Limits they've set" in body
    assert "guardian-accompanied" in body
    assert "17:00" in body
    # Legibility-only — the ward page never exposes an edit/limits action.
    assert f"/wards/{ward.pk}/limits/" not in body
