"""W1-2: identity verification must lapse when the latest age-assurance proof expires.

Before this, ``can_participate`` checked only the ``is_identity_verified`` boolean, so a
child who aged out of a band — or whose attestation went stale — kept a verified status
and cohort forever. See docs/PRODUCTION_HARDENING_PLAN_2026-05.md (W1-2 / PRIV-5)."""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeAssurance, AgeBand, User
from apps.accounts.services import apply_assurance, can_participate, is_assurance_current

pytestmark = pytest.mark.django_db


def _adult(name, expires_at=None):
    u = User.objects.create_user(username=name, password="pw")
    apply_assurance(
        u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev", expires_at=expires_at)
    )
    return u


def test_unexpired_assurance_participates():
    assert can_participate(_adult("ex_future", timezone.now() + timedelta(days=30))) is True


def test_non_expiring_assurance_participates():
    assert can_participate(_adult("ex_none", None)) is True


def test_expired_assurance_blocks_participation():
    u = _adult("ex_past", timezone.now() - timedelta(seconds=1))
    # The denormalized flag is still True — the expiry check is what now blocks access.
    assert u.is_identity_verified is True
    assert is_assurance_current(u) is False
    assert can_participate(u) is False


def test_reverification_restores_eligibility():
    u = _adult("ex_reverify", timezone.now() - timedelta(days=1))
    assert can_participate(u) is False
    apply_assurance(
        u,
        AssuranceResult(
            age_band=AgeBand.ADULT, provider="dev", expires_at=timezone.now() + timedelta(days=30)
        ),
    )
    assert can_participate(u) is True


def test_flag_without_assurance_row_falls_back_true():
    """A staff/legacy account verified out-of-band (no AgeAssurance row) still works."""
    u = User.objects.create_user(username="ex_legacy", password="pw")
    u.age_band = AgeBand.ADULT
    u.recompute_cohort()
    u.is_identity_verified = True
    u.save()
    assert AgeAssurance.objects.filter(user=u).count() == 0
    assert can_participate(u) is True
