"""DSA sanctions hardening — HTTP-layer coverage for the DRF moderation/referral views and the
edge cases the service-level tests don't reach:

  * suspend_days -> expires_at mapping (SUSPEND and TIMED_BAN) through the resolve endpoint;
  * TIMED_BAN duration validation (a timed ban with no duration is rejected, not silently
    turned into a never-lifting permanent deactivation);
  * indefinite SUSPEND (no duration) deactivates and is *intentionally* never auto-lifted;
  * authority-referral create returns the proof pack and never notifies the subject;
  * the proof-view GET is itself audited;
  * permission gating (moderator-only) on resolve / referral;
  * the concurrency-hardened lift reactivates a multi-restriction account exactly once.

The service-level sanction machinery is covered in test_phase2_sanctions / test_appeals /
test_enlargements; this file is deliberately surface-focused (APIClient) plus the lift dedup.
"""

import datetime as dt

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Role, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.safety.models import (
    AuditLog,
    AuthorityReferral,
    ModerationAction,
    ReasonCode,
    Report,
)
from apps.safety.services import file_report, lift_expired_suspensions, take_action

pytestmark = pytest.mark.django_db


def _user(name, role=Role.USER, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name, role=role)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _mod_client(name="hmod"):
    mod = _user(name, role=Role.MODERATOR)
    client = APIClient()
    client.force_authenticate(mod)
    return mod, client


def _open_report(offender, reason=ReasonCode.HARASSMENT):
    return file_report(_user(f"rep-{offender.username}"), offender, reason)


# --- resolve endpoint: suspend_days -> expires_at -----------------------------------------


def test_resolve_suspend_with_days_maps_to_expires_at():
    _, client = _mod_client("m_susp")
    offender = _user("off_susp")
    report = _open_report(offender)

    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "suspend", "reason": ReasonCode.HARASSMENT, "suspend_days": 7},
        format="json",
    )
    assert resp.status_code == 200, resp.content

    action = ModerationAction.objects.get(action=ModerationAction.Action.SUSPEND)
    assert action.expires_at is not None
    # ~7 days out (allow a generous window so wall-clock during the request can't flake it).
    delta = action.expires_at - timezone.now()
    assert dt.timedelta(days=6, hours=23) < delta < dt.timedelta(days=7, hours=1)
    offender.refresh_from_db()
    assert offender.is_active is False


def test_resolve_timed_ban_with_days_maps_to_expires_at():
    _, client = _mod_client("m_tb")
    offender = _user("off_tb")
    report = _open_report(offender)

    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "timed_ban", "reason": ReasonCode.HARASSMENT, "suspend_days": 3},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    action = ModerationAction.objects.get(action=ModerationAction.Action.TIMED_BAN)
    assert action.expires_at is not None
    delta = action.expires_at - timezone.now()
    assert dt.timedelta(days=2, hours=23) < delta < dt.timedelta(days=3, hours=1)
    offender.refresh_from_db()
    assert offender.is_active is False


def test_resolve_timed_ban_without_days_is_rejected():
    # A timed ban with no duration must be a 400 — never a silent never-lifting permanent
    # deactivation that lives outside the BAN identity ledger.
    _, client = _mod_client("m_tbx")
    offender = _user("off_tbx")
    report = _open_report(offender)

    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "timed_ban", "reason": ReasonCode.HARASSMENT},
        format="json",
    )
    assert resp.status_code == 400, resp.content
    assert "suspend_days" in resp.json()
    # No action taken, account untouched.
    assert not ModerationAction.objects.filter(action=ModerationAction.Action.TIMED_BAN).exists()
    offender.refresh_from_db()
    assert offender.is_active is True
    report.refresh_from_db()
    assert report.status == Report.Status.OPEN


def test_resolve_suspend_without_days_is_indefinite_and_never_auto_lifts():
    # Documented edge: a SUSPEND with no suspend_days is an *indefinite* deactivation
    # (expires_at = NULL). It is intentionally NOT auto-lifted by the nightly job — only a
    # manual lift / overturned appeal reactivates it. (See docs/RUNBOOK.md.)
    _, client = _mod_client("m_ind")
    offender = _user("off_ind")
    report = _open_report(offender)

    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "suspend", "reason": ReasonCode.HARASSMENT},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    action = ModerationAction.objects.get(action=ModerationAction.Action.SUSPEND)
    assert action.expires_at is None
    offender.refresh_from_db()
    assert offender.is_active is False

    # The auto-lift job leaves an indefinite suspension in place (no expiry to elapse).
    assert lift_expired_suspensions() == 0
    offender.refresh_from_db()
    assert offender.is_active is False


def test_resolve_requires_moderator():
    offender = _user("off_perm")
    report = _open_report(offender)
    payload = {"decision": "suspend", "reason": ReasonCode.HARASSMENT, "suspend_days": 1}

    # Authenticated non-moderator -> 403.
    plain = APIClient()
    plain.force_authenticate(_user("plain_perm"))
    assert (
        plain.post(
            f"/api/safety/moderation/reports/{report.id}/resolve/", payload, format="json"
        ).status_code
        == 403
    )
    # Anonymous -> 401/403, never 200.
    assert APIClient().post(
        f"/api/safety/moderation/reports/{report.id}/resolve/", payload, format="json"
    ).status_code in (401, 403)
    # Untouched.
    offender.refresh_from_db()
    assert offender.is_active is True
    assert not ModerationAction.objects.exists()


# --- authority referral surface -----------------------------------------------------------


def test_referral_create_returns_proof_pack_and_does_not_notify_subject():
    _, client = _mod_client("m_ref")
    subject = _user("subj_ref")

    resp = client.post(
        "/api/safety/moderation/referrals/",
        {
            "subject": str(subject.public_id),
            "reason": ReasonCode.GROOMING,
            "authority": AuthorityReferral.Authority.IGPR,
            "reference": "case-77",
        },
        format="json",
    )
    assert resp.status_code == 201, resp.content
    body = resp.json()
    assert body["subject_ref"] == str(subject.public_id)
    assert body["reference"] == "case-77"
    assert body["chain_valid"] is True
    assert body["anchor_hash"]

    # Deliberately silent to the subject (tipping off a suspect can defeat an investigation).
    assert Notification.objects.filter(recipient=subject).count() == 0
    # And it is recorded in the tamper-evident log.
    assert AuditLog.objects.filter(event="authority.referral").exists()


def test_referral_unknown_subject_is_404():
    _, client = _mod_client("m_ref404")
    resp = client.post(
        "/api/safety/moderation/referrals/",
        {
            "subject": "00000000-0000-0000-0000-000000000000",
            "reason": ReasonCode.GROOMING,
            "authority": AuthorityReferral.Authority.IGPR,
        },
        format="json",
    )
    assert resp.status_code == 404, resp.content
    assert not AuthorityReferral.objects.exists()


def test_referral_proof_view_is_audited():
    mod, client = _mod_client("m_proof")
    subject = _user("subj_proof")
    create = client.post(
        "/api/safety/moderation/referrals/",
        {
            "subject": str(subject.public_id),
            "reason": ReasonCode.CSAM,
            "authority": AuthorityReferral.Authority.INHOPE,
        },
        format="json",
    )
    assert create.status_code == 201, create.content
    referral = AuthorityReferral.objects.get()

    before = AuditLog.objects.filter(event="authority.referral_proof_viewed").count()
    resp = client.get(f"/api/safety/moderation/referrals/{referral.id}/proof/")
    assert resp.status_code == 200, resp.content
    assert resp.json()["chain_valid"] is True
    after = AuditLog.objects.filter(event="authority.referral_proof_viewed").count()
    # A lawful-request proof view is itself logged (who looked, when).
    assert after == before + 1


def test_referral_requires_moderator():
    subject = _user("subj_perm")
    payload = {
        "subject": str(subject.public_id),
        "reason": ReasonCode.GROOMING,
        "authority": AuthorityReferral.Authority.IGPR,
    }
    plain = APIClient()
    plain.force_authenticate(_user("plain_ref"))
    denied = plain.post("/api/safety/moderation/referrals/", payload, format="json")
    assert denied.status_code == 403
    assert APIClient().post(
        "/api/safety/moderation/referrals/", payload, format="json"
    ).status_code in (401, 403)
    assert not AuthorityReferral.objects.exists()


def test_referral_proof_view_requires_moderator():
    # The lawful-request proof bundle (subject_ref + chain validity + anchor) is moderator-only;
    # a non-moderator must not be able to read or enumerate it.
    mod, client = _mod_client("m_proofperm")
    subject = _user("subj_proofperm")
    create = client.post(
        "/api/safety/moderation/referrals/",
        {
            "subject": str(subject.public_id),
            "reason": ReasonCode.GROOMING,
            "authority": AuthorityReferral.Authority.IGPR,
        },
        format="json",
    )
    assert create.status_code == 201, create.content
    referral = AuthorityReferral.objects.get()
    url = f"/api/safety/moderation/referrals/{referral.id}/proof/"

    plain = APIClient()
    plain.force_authenticate(_user("plain_proofperm"))
    assert plain.get(url).status_code == 403
    assert APIClient().get(url).status_code in (401, 403)
    # The gate denies before the proof view's own audit row is written.
    assert not AuditLog.objects.filter(event="authority.referral_proof_viewed").exists()


# --- concurrency-hardened lift: dedup across multiple expiries on one account --------------


def test_lift_reactivates_multi_restriction_account_exactly_once():
    # An account with two *separately* expired timed restrictions must be reactivated once —
    # one suspension_lifted audit entry and one dignity notice, not two. This exercises the
    # account-row lock + the `not is_active` guard in the hardened lift.
    mod = _user("m_multi", role=Role.MODERATOR)
    offender = _user("off_multi")
    past1 = timezone.now() - dt.timedelta(hours=2)
    past2 = timezone.now() - dt.timedelta(hours=1)
    take_action(mod, offender, ModerationAction.Action.SUSPEND, ReasonCode.SPAM, expires_at=past1)
    take_action(
        mod, offender, ModerationAction.Action.TIMED_BAN, ReasonCode.HARASSMENT, expires_at=past2
    )
    offender.refresh_from_db()
    assert offender.is_active is False
    # Count only the end-of-suspension notice.
    Notification.objects.filter(recipient=offender).delete()

    assert lift_expired_suspensions() == 1
    offender.refresh_from_db()
    assert offender.is_active is True
    assert AuditLog.objects.filter(event="moderation.suspension_lifted").count() == 1
    assert (
        Notification.objects.filter(recipient=offender, kind=Notification.Kind.MODERATION).count()
        == 1
    )
    # Both restrictions are now marked lifted, so a second pass is a no-op.
    assert ModerationAction.objects.filter(lifted_at__isnull=True).count() == 0
    assert lift_expired_suspensions() == 0
