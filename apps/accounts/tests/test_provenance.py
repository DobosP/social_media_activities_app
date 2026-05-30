"""F14: age-proof provenance helper — band/method/timestamps only, never identity/DOB/raw."""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeAssurance, AgeBand, User
from apps.accounts.services import apply_assurance, assurance_provenance

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_none_when_unverified():
    u = User.objects.create_user(username="pv_unv", password="pw")
    assert assurance_provenance(u) is None


def test_provenance_exposes_no_pii():
    p = assurance_provenance(_user("pv_u"))
    assert p["has_row"] is True
    assert p["band_display"]  # a human label, not a raw token
    # The raw attestation and the raw band value must never be surfaced.
    assert "raw" not in p
    assert "age_band" not in p
    assert "dob" not in p and "date_of_birth" not in p


def test_expired_status():
    u = _user("pv_exp")
    AgeAssurance.objects.filter(user=u).update(expires_at=timezone.now() - timedelta(days=1))
    p = assurance_provenance(u)
    assert p["status"] == "expired"
    assert p["is_current"] is False
    assert p["expires_soon"] is False


def test_expiring_soon_status():
    u = _user("pv_soon")
    AgeAssurance.objects.filter(user=u).update(expires_at=timezone.now() + timedelta(days=3))
    p = assurance_provenance(u)
    assert p["status"] == "expiring"
    assert p["expires_soon"] is True
    assert 0 <= p["days_left"] <= 14
