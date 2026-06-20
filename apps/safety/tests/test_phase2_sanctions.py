"""Phase 2: timed/lifetime sanctions, lifetime-ban identity ledger, authority referral.

Reuses the existing take_action / lift_expired_suspensions / hash-chained AuditLog machinery.
"""

import datetime as dt

import pytest
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, BannedIdentity, IdentityBinding, User
from apps.accounts.services import (
    IdentityBanned,
    apply_assurance,
    bind_identity,
    erase_user,
)
from apps.safety.models import AuthorityReferral, ModerationAction, ReasonCode
from apps.safety.services import (
    create_authority_referral,
    lift_expired_suspensions,
    referral_proof_pack,
    safety_record_for,
    take_action,
)

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _verified_result(sub="holder-abc"):
    return AssuranceResult(
        age_band=AgeBand.ADULT,
        verified=True,
        provider="eudi",
        method="openid4vp",
        holder_sub=sub,
        raw={
            "age_over_16": True,
            "age_over_18": True,
            "format": "jwt_vc",
            "holder_proof": "verified",
        },
    )


# --- timed ban: deactivate + auto-lift ---


def test_timed_ban_deactivates_and_auto_lifts_on_expiry():
    mod, offender = _user("mod"), _user("bad")
    past = timezone.now() - dt.timedelta(hours=1)
    take_action(
        mod, offender, ModerationAction.Action.TIMED_BAN, ReasonCode.HARASSMENT, expires_at=past
    )
    offender.refresh_from_db()
    assert offender.is_active is False

    assert lift_expired_suspensions() == 1
    offender.refresh_from_db()
    assert offender.is_active is True


def test_timed_ban_not_lifted_while_a_newer_restriction_is_active():
    mod, offender = _user("mod2"), _user("bad2")
    past = timezone.now() - dt.timedelta(hours=1)
    future = timezone.now() + dt.timedelta(days=3)
    # An elapsed timed ban, but a still-active suspension also applies -> stays deactivated.
    take_action(
        mod, offender, ModerationAction.Action.TIMED_BAN, ReasonCode.HARASSMENT, expires_at=past
    )
    take_action(
        mod, offender, ModerationAction.Action.SUSPEND, ReasonCode.HARASSMENT, expires_at=future
    )
    assert lift_expired_suspensions() == 0
    offender.refresh_from_db()
    assert offender.is_active is False


def test_plain_suspension_still_lifts():
    mod, offender = _user("mod3"), _user("bad3")
    past = timezone.now() - dt.timedelta(hours=1)
    take_action(mod, offender, ModerationAction.Action.SUSPEND, ReasonCode.SPAM, expires_at=past)
    assert lift_expired_suspensions() == 1
    offender.refresh_from_db()
    assert offender.is_active is True


# --- lifetime ban: identity ledger survives erasure and blocks re-registration ---


def test_lifetime_ban_blocks_wallet_re_registration(settings):
    settings.IDENTITY_UNIQUENESS_ENFORCED = True
    mod = _user("mod4")
    offender = User.objects.create_user(username="bad4", password="pw")
    bind_identity(offender, _verified_result(sub="holder-banned"))

    take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    offender.refresh_from_db()
    assert offender.is_active is False
    assert BannedIdentity.objects.filter(holder_hash=IdentityBinding.objects.get().holder_hash)

    # Even after erasing the account, the same wallet cannot register a fresh one.
    erase_user(offender, offender)
    newcomer = User.objects.create_user(username="fresh4", password="pw")
    with pytest.raises(IdentityBanned):
        bind_identity(newcomer, _verified_result(sub="holder-banned"))


def test_lifetime_ban_without_binding_is_a_noop_on_the_ledger():
    # A user who was never wallet-verified has no holder hash to ban; the account-level ban
    # still applies, but nothing is written to the identity ledger.
    mod, offender = _user("mod5"), _user("bad5")
    take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    offender.refresh_from_db()
    assert offender.is_active is False
    assert BannedIdentity.objects.count() == 0


# --- authority referral + tamper-evident proof pack ---


def test_authority_referral_pins_anchor_and_proof_validates():
    mod, subject = _user("mod6"), _user("subj6")
    referral = create_authority_referral(
        mod,
        subject,
        ReasonCode.GROOMING,
        authority=AuthorityReferral.Authority.IGPR,
        reference="case-123",
    )
    # Subject is identified by public_id (impersonation-safe, survives erasure).
    assert referral.subject_ref == subject.public_id
    assert referral.audit_anchor_hash  # pinned to the chain tip

    pack = referral_proof_pack(referral)
    assert pack["chain_valid"] is True
    assert pack["subject_ref"] == str(subject.public_id)
    assert pack["authority"] == AuthorityReferral.Authority.IGPR.label
    # The referral itself is recorded in the tamper-evident log.
    from apps.safety.models import AuditLog

    assert AuditLog.objects.filter(event="authority.referral").exists()


def test_proof_pack_detects_audit_tampering():
    from apps.safety.models import AuditLog

    mod, subject = _user("mod7"), _user("subj7")
    referral = create_authority_referral(
        mod, subject, ReasonCode.CSAM, authority=AuthorityReferral.Authority.INHOPE
    )
    row = AuditLog.objects.order_by("id").first()
    row.data = {"event": "altered"}
    row.save(update_fields=["data"])
    assert referral_proof_pack(referral)["chain_valid"] is False


# --- F19 self-record surfaces the subject's own sanctions ---


def test_safety_record_shows_timed_and_lifetime_ban():
    mod, offender = _user("mod8"), _user("bad8")
    take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    record = safety_record_for(offender)
    sanctions = [d for d in record["decisions"] if d["is_sanction"]]
    assert sanctions and sanctions[0]["is_active"] is True
