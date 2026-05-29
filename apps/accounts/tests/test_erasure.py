"""Tests for GDPR Art.17 right-to-erasure (W1-5): a user can erase their own account and
a guardian can erase a ward's; strangers cannot; the deletion is recorded in the
tamper-evident audit log BEFORE the row disappears (using the target's public_id), and the
chain stays valid afterwards. See docs/COMPLIANCE.md."""

import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import (
    apply_assurance,
    can_participate,
    erase_user,
    grant_parental_consent,
    link_guardian,
)
from apps.safety.models import AuditLog
from apps.safety.services import verify_audit_chain

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_self_erasure_deletes_account_and_audits():
    user = _user("erase_me")
    public_id = str(user.public_id)
    uid = user.id

    erase_user(user, user)

    assert not User.objects.filter(id=uid).exists()
    entry = AuditLog.objects.get(event="account.erased")
    assert entry.data["erased_public_id"] == public_id
    # The username is NOT retained in the permanent log after erasure (only the UUID).
    assert "erased_username" not in entry.data
    assert verify_audit_chain() is True


def test_erasing_guardian_revokes_wards_consent():
    """A guardian self-erasing must not leave the ward able to participate off a consent
    whose guardian no longer exists (the consent is string-referenced, not an FK)."""
    guardian = _user("g_self_erase")
    child = _user("w_left_behind", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    grant_parental_consent(guardian, child)
    assert can_participate(child) is True

    erase_user(guardian, guardian)

    child.refresh_from_db()
    assert can_participate(child) is False
    assert not ParentalConsent.objects.filter(
        minor=child, status=ParentalConsent.Status.ACTIVE
    ).exists()


def test_guardian_can_erase_ward():
    guardian = _user("g_erase")
    child = _user("w_erase", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    child_id = child.id

    erase_user(guardian, child)

    assert not User.objects.filter(id=child_id).exists()
    # The guardian's own account survives.
    guardian.refresh_from_db()
    entry = AuditLog.objects.get(event="account.erased")
    assert entry.actor_id == guardian.id
    assert entry.data["erased_public_id"]


def test_stranger_cannot_erase():
    stranger = _user("stranger_erase")
    victim = _user("victim_erase")
    with pytest.raises(ValueError):
        erase_user(stranger, victim)
    assert User.objects.filter(id=victim.id).exists()
    assert not AuditLog.objects.filter(event="account.erased").exists()


def test_non_guardian_adult_cannot_erase_minor():
    other = _user("other_adult")
    child = _user("child_protected", AgeBand.UNDER_16)
    with pytest.raises(ValueError):
        erase_user(other, child)
    assert User.objects.filter(id=child.id).exists()


def test_me_delete_self_erases():
    user = _user("api_self_erase")
    uid = user.id
    client = APIClient()
    client.force_authenticate(user)

    resp = client.delete("/api/accounts/me/")
    assert resp.status_code == 204
    assert not User.objects.filter(id=uid).exists()


def test_ward_delete_erases_ward():
    guardian = _user("api_g_erase")
    child = _user("api_w_erase", AgeBand.UNDER_16)
    link_guardian(guardian, child)
    child_id = child.id
    client = APIClient()
    client.force_authenticate(guardian)

    resp = client.delete(f"/api/accounts/wards/{child.public_id}/")
    assert resp.status_code == 204
    assert not User.objects.filter(id=child_id).exists()


def test_ward_delete_rejects_non_guardian():
    other = _user("api_stranger_erase")
    child = _user("api_protected", AgeBand.UNDER_16)
    client = APIClient()
    client.force_authenticate(other)

    resp = client.delete(f"/api/accounts/wards/{child.public_id}/")
    assert resp.status_code == 403
    assert User.objects.filter(id=child.id).exists()
