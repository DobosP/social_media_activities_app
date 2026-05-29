"""Tests for the parental-consent management flow (ACCT Wave 0 fix): a verified adult
guardian can grant/revoke consent for an under-16 ward through the service and the API,
non-adults cannot be guardians, and revocation removes participation eligibility. Before
this flow existed, only Django admin could create a consent record, blocking all real
minor onboarding. See docs/AUDIT_2026-05.md."""

import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import (
    apply_assurance,
    can_participate,
    grant_parental_consent,
    link_guardian,
    revoke_parental_consent,
)

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_guardian_must_be_an_adult():
    child = _user("c_minor", AgeBand.UNDER_16)
    teen = _user("c_teen", AgeBand.AGE_16_17)
    with pytest.raises(ValueError):
        link_guardian(teen, child)


def test_grant_consent_makes_minor_eligible():
    guardian = _user("g_adult", AgeBand.ADULT)
    child = _user("w_minor", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    assert can_participate(child) is False  # no consent yet
    grant_parental_consent(guardian, child)
    assert can_participate(child) is True
    assert ParentalConsent.objects.filter(
        minor=child, status=ParentalConsent.Status.ACTIVE
    ).exists()


def test_non_guardian_cannot_grant_consent():
    stranger = _user("g_stranger", AgeBand.ADULT)
    child = _user("w_minor2", AgeBand.UNDER_16)
    with pytest.raises(ValueError):
        grant_parental_consent(stranger, child)


def test_revoke_consent_makes_minor_ineligible():
    guardian = _user("g_adult2", AgeBand.ADULT)
    child = _user("w_minor3", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    grant_parental_consent(guardian, child)
    assert can_participate(child) is True
    revoke_parental_consent(guardian, child)
    assert can_participate(child) is False


def test_consent_api_grant_and_revoke():
    guardian = _user("g_api", AgeBand.ADULT)
    child = _user("w_api", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    client = APIClient()
    client.force_authenticate(guardian)
    url = f"/api/accounts/wards/{child.public_id}/consent/"

    granted = client.post(url)
    assert granted.status_code == 201, granted.content
    assert can_participate(child) is True

    revoked = client.delete(url)
    assert revoked.status_code == 204
    assert can_participate(child) is False


def test_consent_api_rejects_non_guardian():
    stranger = _user("g_api_stranger", AgeBand.ADULT)
    child = _user("w_api2", AgeBand.UNDER_16)
    client = APIClient()
    client.force_authenticate(stranger)
    resp = client.post(f"/api/accounts/wards/{child.public_id}/consent/")
    assert resp.status_code == 400
