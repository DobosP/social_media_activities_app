"""W1-1: the guardian-link onboarding flow.

Before this, `link_guardian` had ZERO non-test callers, so a minor could register but
never reach `can_participate=True` — the whole minor journey was dead-ended. These tests
exercise the REAL mutually-confirmed invite/accept flow (no `link_guardian` shortcut)
across the service, API, and web layers, ending in a participating minor. See
docs/PRODUCTION_HARDENING_PLAN_2026-05.md (W1-1)."""

from datetime import timedelta

import pytest
from django.test import Client, override_settings
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, GuardianLinkInvite, User
from apps.accounts.services import (
    accept_guardian_link_invite,
    apply_assurance,
    can_participate,
    create_guardian_link_invite,
    decline_guardian_link_invite,
    grant_parental_consent,
    is_guardian_of,
)

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


# --- service layer ------------------------------------------------------------------


def test_only_verified_adult_can_invite():
    teen = _user("gl_teen", AgeBand.AGE_16_17)
    child = _user("gl_child", AgeBand.UNDER_16)
    with pytest.raises(ValueError):
        create_guardian_link_invite(teen, child)


def test_cannot_invite_an_adult_as_ward():
    guardian = _user("gl_g1", AgeBand.ADULT)
    other_adult = _user("gl_a2", AgeBand.ADULT)
    with pytest.raises(ValueError):
        create_guardian_link_invite(guardian, other_adult)


def test_accept_creates_link_and_enables_participation_after_consent():
    guardian = _user("gl_g2", AgeBand.ADULT)
    child = _user("gl_c2", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    assert invite.status == GuardianLinkInvite.Status.PENDING
    assert is_guardian_of(guardian, child) is False  # not linked until accepted

    accept_guardian_link_invite(child, invite.token)
    assert is_guardian_of(guardian, child) is True
    assert can_participate(child) is False  # link alone isn't consent

    # The existing Wave-0 consent service now has a real link to act on.
    grant_parental_consent(guardian, child)
    assert can_participate(child) is True


def test_only_the_addressed_ward_can_accept():
    guardian = _user("gl_g3", AgeBand.ADULT)
    child = _user("gl_c3", AgeBand.UNDER_16)
    intruder = _user("gl_x3", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    with pytest.raises(ValueError):
        accept_guardian_link_invite(intruder, invite.token)
    assert is_guardian_of(guardian, child) is False


def test_expired_invite_cannot_be_accepted():
    guardian = _user("gl_g4", AgeBand.ADULT)
    child = _user("gl_c4", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    invite.expires_at = timezone.now() - timedelta(seconds=1)
    invite.save(update_fields=["expires_at"])
    with pytest.raises(ValueError):
        accept_guardian_link_invite(child, invite.token)
    invite.refresh_from_db()
    assert invite.status == GuardianLinkInvite.Status.EXPIRED
    assert is_guardian_of(guardian, child) is False


def test_accept_revalidates_inviter_is_still_adult():
    guardian = _user("gl_g5", AgeBand.ADULT)
    child = _user("gl_c5", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    # Inviter is no longer an adult by accept time → link_guardian re-check blocks it.
    guardian.cohort = Cohort.TEEN
    guardian.save(update_fields=["cohort"])
    with pytest.raises(ValueError):
        accept_guardian_link_invite(child, invite.token)
    assert is_guardian_of(guardian, child) is False


def test_accept_revalidates_inviter_assurance_not_expired():
    """A still-ADULT-cohort guardian whose age assurance has LAPSED cannot be linked."""
    guardian = _user("gl_g7", AgeBand.ADULT)
    child = _user("gl_c7", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    # Expire the guardian's assurance (cohort stays ADULT, but can_participate is now False).
    a = guardian.age_assurances.latest("verified_at")
    a.expires_at = timezone.now() - timedelta(days=1)
    a.save(update_fields=["expires_at"])
    with pytest.raises(ValueError):
        accept_guardian_link_invite(child, invite.token)
    assert is_guardian_of(guardian, child) is False


def test_cannot_invite_unassigned_account_as_ward():
    guardian = _user("gl_g8", AgeBand.ADULT)
    unknown = _user("gl_u8", AgeBand.UNKNOWN)  # cohort UNASSIGNED
    with pytest.raises(ValueError):
        create_guardian_link_invite(guardian, unknown)


@override_settings(ALLOW_MINOR_ONBOARDING=False)
def test_minor_onboarding_disabled_blocks_guardian_link():
    """Production default: minors cannot be onboarded (no verifiable trust anchor yet)."""
    guardian = _user("gl_off_g", AgeBand.ADULT)
    child = _user("gl_off_c", AgeBand.UNDER_16)
    with pytest.raises(ValueError):
        create_guardian_link_invite(guardian, child)


def test_minor_onboarding_disabled_blocks_consent_grant():
    from apps.accounts.services import grant_parental_consent, link_guardian

    guardian = _user("gl_off2_g", AgeBand.ADULT)
    child = _user("gl_off2_c", AgeBand.UNDER_16)
    link_guardian(guardian, child)  # set up a link while onboarding is enabled
    with override_settings(ALLOW_MINOR_ONBOARDING=False), pytest.raises(ValueError):
        grant_parental_consent(guardian, child)


def test_accept_writes_audit_entry():
    """accept must record a tamper-evident audit entry (and, per the fix, do so INSIDE its
    atomic block — record_audit takes a row lock that raises outside a transaction on
    PostgreSQL in real-request autocommit). A dedicated transaction=True test would
    reproduce the prod condition but collides with the other TransactionTestCase suites on
    content-type re-insertion, so we assert the durable side effect instead."""
    from apps.safety.models import AuditLog

    guardian = _user("gl_audit_g", AgeBand.ADULT)
    child = _user("gl_audit_c", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    accept_guardian_link_invite(child, invite.token)
    assert AuditLog.objects.filter(event="guardian.link_accepted").exists()


def test_decline_leaves_no_link():
    guardian = _user("gl_g6", AgeBand.ADULT)
    child = _user("gl_c6", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)
    decline_guardian_link_invite(child, invite.token)
    invite.refresh_from_db()
    assert invite.status == GuardianLinkInvite.Status.DECLINED
    assert is_guardian_of(guardian, child) is False


# --- API layer ----------------------------------------------------------------------


def test_api_full_onboarding_flow():
    guardian = _user("gl_api_g", AgeBand.ADULT)
    child = _user("gl_api_c", AgeBand.UNDER_16)

    gc = APIClient()
    gc.force_authenticate(guardian)
    created = gc.post(
        "/api/accounts/guardian-links/", {"ward": str(child.public_id)}, format="json"
    )
    assert created.status_code == 201, created.content
    token = created.json()["token"]

    cc = APIClient()
    cc.force_authenticate(child)
    pending = cc.get("/api/accounts/guardian-links/")
    assert pending.status_code == 200
    assert any(i["token"] == token for i in pending.json())

    accepted = cc.post(f"/api/accounts/guardian-links/{token}/accept/")
    assert accepted.status_code == 200, accepted.content
    assert is_guardian_of(guardian, child) is True

    # Guardian then grants consent via the existing endpoint → minor can participate.
    granted = gc.post(f"/api/accounts/wards/{child.public_id}/consent/")
    assert granted.status_code == 201, granted.content
    assert can_participate(child) is True


def test_api_non_ward_cannot_accept():
    guardian = _user("gl_api_g2", AgeBand.ADULT)
    child = _user("gl_api_c2", AgeBand.UNDER_16)
    intruder = _user("gl_api_x2", AgeBand.UNDER_16)
    invite = create_guardian_link_invite(guardian, child)

    ic = APIClient()
    ic.force_authenticate(intruder)
    resp = ic.post(f"/api/accounts/guardian-links/{invite.token}/accept/")
    assert resp.status_code == 400
    assert is_guardian_of(guardian, child) is False


# --- web layer ----------------------------------------------------------------------


def test_web_invite_and_accept_flow():
    guardian = _user("gl_web_g", AgeBand.ADULT)
    child = _user("gl_web_c", AgeBand.UNDER_16)

    gc = Client()
    gc.force_login(guardian)
    r = gc.post("/wards/invite/", {"ward_username": child.username, "relationship": "parent"})
    assert r.status_code == 302  # redirects back to /wards with the code in a message
    invite = GuardianLinkInvite.objects.get(guardian=guardian, ward=child)

    cc = Client()
    cc.force_login(child)
    r2 = cc.post(f"/guardian-invites/{invite.token}/accept/")
    assert r2.status_code == 302
    assert is_guardian_of(guardian, child) is True
