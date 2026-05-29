import secrets
from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.utils import timezone

from .models import (
    COHORT_BY_AGE_BAND,
    AgeAssurance,
    Cohort,
    GuardianLinkInvite,
    GuardianRelationship,
    ParentalConsent,
    User,
)


def assign_cohort(age_band: str) -> str:
    return COHORT_BY_AGE_BAND.get(age_band, Cohort.UNASSIGNED)


def apply_assurance(user: User, result) -> AgeAssurance:
    """Persist an assurance result onto the user and record it. Does NOT by itself
    grant participation for minors — that still requires valid parental consent."""
    old_cohort = user.cohort
    user.age_band = result.age_band
    user.recompute_cohort()
    user.is_identity_verified = bool(result.verified)
    user.identity_verified_at = timezone.now() if result.verified else None
    user.save(update_fields=["age_band", "cohort", "is_identity_verified", "identity_verified_at"])
    # A cohort change on re-verification must evict the user from conversations pinned to
    # their OLD cohort (cohort isolation): every such conversation is now cross-cohort for
    # them. (First verification from UNASSIGNED has no prior conversations to clean.)
    if user.cohort != old_cohort and old_cohort != Cohort.UNASSIGNED:
        from apps.messaging.services import remove_user_from_conversations

        remove_user_from_conversations(user, reason="cohort_changed")
    return AgeAssurance.objects.create(
        user=user,
        provider=result.provider,
        method=result.method,
        age_band=result.age_band,
        expires_at=result.expires_at,
        raw=result.raw,
    )


def has_valid_parental_consent(user: User) -> bool:
    return any(consent.is_valid() for consent in user.parental_consents.all())


def is_assurance_current(user: User) -> bool:
    """Identity verification is only valid while the *latest* age-assurance proof is
    unexpired. A proof with no expiry never lapses; an expired proof means the user must
    re-verify — so a child who ages out of a band, or whose attestation has gone stale,
    can no longer participate (join/post/chat) until they re-verify. (Cohort is not
    recomputed here; it is re-derived on the next successful assurance.)

    Falls back to the denormalized ``is_identity_verified`` flag when no assurance row
    exists (e.g. a staff/legacy account verified out-of-band)."""
    if not user.is_identity_verified:
        return False
    latest = AgeAssurance.objects.filter(user=user).order_by("-verified_at", "-id").first()
    if latest is None:
        return True
    return latest.expires_at is None or latest.expires_at > timezone.now()


def can_participate(user: User) -> bool:
    """The gate D3/D4 uses: identity-verified with a *current* (unexpired) age
    assurance, and (if under 16) a valid parental consent on file."""
    if not is_assurance_current(user):
        return False
    if user.requires_parental_consent:
        return has_valid_parental_consent(user)
    return True


@transaction.atomic
def link_guardian(guardian: User, ward: User, *, relationship="parent", consent=None):
    """Record (or re-activate) an account-level guardianship link guardian → ward."""
    if guardian.id == ward.id:
        raise ValueError("A user cannot be their own guardian.")
    if guardian.cohort != Cohort.ADULT:
        # A guardian is an adult protector of a minor; never a child/teen/unassigned user.
        raise ValueError("A guardian must be a verified adult.")
    link, _ = GuardianRelationship.objects.update_or_create(
        guardian=guardian,
        ward=ward,
        defaults={
            "relationship": relationship,
            "consent": consent,
            "status": GuardianRelationship.Status.ACTIVE,
        },
    )
    return link


def pending_guardian_invites_for(ward: User):
    """Open, unexpired guardian-link invites awaiting this ward's response."""
    return GuardianLinkInvite.objects.filter(
        ward=ward, status=GuardianLinkInvite.Status.PENDING, expires_at__gt=timezone.now()
    ).select_related("guardian")


@transaction.atomic
def create_guardian_link_invite(
    guardian: User, ward: User, *, relationship: str = "parent"
) -> GuardianLinkInvite:
    """A verified adult invites a (minor) ward to confirm a guardianship link.

    The link is NOT created here — the ward must accept (see accept_guardian_link_invite),
    so the relationship requires both parties to act. Raises ValueError on any precondition
    failure."""
    if guardian.id == ward.id:
        raise ValueError("A user cannot be their own guardian.")
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise ValueError("Only a verified adult can invite a ward.")
    if ward.cohort not in (Cohort.CHILD, Cohort.TEEN):
        # Minor-only: also rejects ADULT and the UNASSIGNED (unverified/unknown-age) cohort.
        raise ValueError("A guardian link can only target a minor account.")
    if is_guardian_of(guardian, ward):
        raise ValueError("You are already this user's guardian.")
    ttl_days = getattr(settings, "GUARDIAN_INVITE_TTL_DAYS", 7)
    # Idempotent per pair: refresh the open invite rather than stacking duplicates
    # (the partial unique constraint also enforces at most one PENDING invite per pair).
    invite, _ = GuardianLinkInvite.objects.update_or_create(
        guardian=guardian,
        ward=ward,
        status=GuardianLinkInvite.Status.PENDING,
        defaults={
            "relationship": relationship,
            "token": secrets.token_urlsafe(24),
            "expires_at": timezone.now() + timedelta(days=ttl_days),
        },
    )
    from apps.safety.services import record_audit

    record_audit("guardian.link_invited", actor=guardian, target=ward)
    return invite


def accept_guardian_link_invite(ward: User, token: str) -> GuardianRelationship:
    """The ward accepts a pending invite, creating the guardianship link. Re-validates the
    inviter is still a *currently-verified* adult at accept time."""
    from apps.safety.services import record_audit

    # The expiry-marking and the link-creation are in SEPARATE atomic blocks: marking an
    # invite EXPIRED and then raising inside one transaction would roll back the EXPIRED
    # write, so we commit that status, then raise outside the block.
    with transaction.atomic():
        invite = (
            GuardianLinkInvite.objects.select_for_update()
            .filter(token=token, status=GuardianLinkInvite.Status.PENDING)
            .first()
        )
        if invite is None:
            raise ValueError("No such pending invite.")
        if invite.ward_id != ward.id:
            raise ValueError("This invite is addressed to a different user.")
        expired = invite.expires_at <= timezone.now()
        if expired:
            invite.status = GuardianLinkInvite.Status.EXPIRED
            invite.responded_at = timezone.now()
            invite.save(update_fields=["status", "responded_at"])
    if expired:
        raise ValueError("This invite has expired.")

    # One atomic unit for the link + its audit entry (record_audit takes a row lock, so it
    # MUST run inside a transaction — outside one it raises on PostgreSQL). Re-validate the
    # inviter is still a current verified adult (link_guardian only checks the cohort).
    with transaction.atomic():
        if invite.guardian.cohort != Cohort.ADULT or not can_participate(invite.guardian):
            raise ValueError("The inviting guardian is no longer a verified adult.")
        link = link_guardian(invite.guardian, ward, relationship=invite.relationship)
        invite.status = GuardianLinkInvite.Status.ACCEPTED
        invite.responded_at = timezone.now()
        invite.save(update_fields=["status", "responded_at"])
        record_audit("guardian.link_accepted", actor=ward, target=invite.guardian)
    return link


@transaction.atomic
def decline_guardian_link_invite(ward: User, token: str) -> None:
    """The ward declines (or revokes) a pending invite addressed to them."""
    invite = (
        GuardianLinkInvite.objects.select_for_update()
        .filter(token=token, status=GuardianLinkInvite.Status.PENDING, ward=ward)
        .first()
    )
    if invite is None:
        raise ValueError("No such pending invite.")
    invite.status = GuardianLinkInvite.Status.DECLINED
    invite.responded_at = timezone.now()
    invite.save(update_fields=["status", "responded_at"])


@transaction.atomic
def revoke_guardian(guardian: User, ward: User) -> None:
    GuardianRelationship.objects.filter(guardian=guardian, ward=ward).update(
        status=GuardianRelationship.Status.REVOKED
    )
    # Revoking the guardianship must also revoke the parental consent this guardian granted
    # (W2-11), otherwise can_participate(ward) stays True off a consent whose authorizing
    # relationship no longer exists. "No guardian -> no consent."
    ParentalConsent.objects.filter(
        minor=ward,
        guardian_identifier=str(guardian.public_id),
        status=ParentalConsent.Status.ACTIVE,
    ).update(status=ParentalConsent.Status.REVOKED, revoked_at=timezone.now())
    # End any messaging observer presence the (now-revoked) guardianship justified, so an
    # adult cannot keep reading a child's E2EE conversation after the relationship ends.
    from apps.messaging.services import drop_guardian_observers_for, remove_user_from_conversations

    drop_guardian_observers_for(guardian, ward)
    # If revoking this guardian's consent leaves the ward unable to participate (no other
    # active consent), evict them from conversations too — consistent with
    # revoke_parental_consent. A co-guardian's still-valid consent keeps them in.
    if not can_participate(ward):
        remove_user_from_conversations(ward, reason="guardian_revoked")


def is_guardian_of(guardian: User, ward: User) -> bool:
    return GuardianRelationship.objects.filter(
        guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE
    ).exists()


@transaction.atomic
def erase_user(actor: User, target: User) -> None:
    """GDPR Art.17 right-to-erasure (W1-5). Permanently deletes `target` and everything
    that cascades from the account (memberships, photos[blob-cleanup signal], messaging
    participation, consents, guardianships). Only the user themselves or an active guardian
    of the target may erase the account; anyone else raises ValueError.

    The erasure is audited BEFORE deletion (so the tamper-evident log records that it
    happened) using the target's public_id, since the row itself is about to disappear."""
    if not (actor.id == target.id or is_guardian_of(actor, target)):
        raise ValueError("You are not authorized to erase this account.")

    from apps.safety.services import record_audit

    # If the target is a guardian, erasing them must NOT leave a ward able to participate
    # off a consent whose guardian no longer exists. ParentalConsent references the guardian
    # by a string identifier (not an FK), so the CASCADE that removes the GuardianRelationship
    # rows would otherwise orphan an ACTIVE consent. revoke_guardian does the full cleanup
    # (revoke that guardian's consent, drop its observer presence, evict the now-ineligible
    # ward from conversations) before the rows cascade away.
    for ward_id in list(
        GuardianRelationship.objects.filter(
            guardian=target, status=GuardianRelationship.Status.ACTIVE
        ).values_list("ward_id", flat=True)
    ):
        revoke_guardian(target, User.objects.get(id=ward_id))

    # Remove the user from their conversations and DELETE their authored E2EE ciphertext.
    # Message.sender is SET_NULL, so target.delete() alone would leave the user's messages
    # (and recipients' wrapped keys) decryptable in others' histories — not true erasure.
    from apps.messaging.models import Message
    from apps.messaging.services import remove_user_from_conversations

    remove_user_from_conversations(target, reason="account_erased")
    Message.objects.filter(sender=target).delete()

    # erased_public_id (a UUID pseudonym) is sufficient to record the event; we do NOT keep
    # the username in the permanent log after erasure.
    record_audit("account.erased", actor=actor, erased_public_id=str(target.public_id))
    target.delete()


@transaction.atomic
def grant_parental_consent(
    guardian: User, ward: User, *, scope="", expires_at=None
) -> ParentalConsent:
    """A verified adult guardian grants parental consent for their under-16 ward.

    Requires an existing active guardianship (established through the verified
    parental-consent identity flow). Activates/refreshes the ward's consent record so
    can_participate(ward) becomes True. Raises ValueError on any precondition failure.
    """
    if not ward.requires_parental_consent:
        raise ValueError("This user does not require parental consent.")
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise ValueError("Only a verified adult guardian can grant consent.")
    if not is_guardian_of(guardian, ward):
        raise ValueError("You are not a registered guardian of this user.")
    consent, _ = ParentalConsent.objects.update_or_create(
        minor=ward,
        guardian_identifier=str(guardian.public_id),
        defaults={
            "status": ParentalConsent.Status.ACTIVE,
            "scope": scope,
            "granted_at": timezone.now(),
            "expires_at": expires_at,
            "revoked_at": None,
        },
    )
    GuardianRelationship.objects.filter(
        guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE
    ).update(consent=consent)
    return consent


@transaction.atomic
def revoke_parental_consent(guardian: User, ward: User) -> int:
    """Revoke all active parental consents for `ward`. "No consent -> no access": the ward
    is removed from messaging conversations (write-path consent re-checks block any new
    participation across the app). Returns the number of consents revoked."""
    if not is_guardian_of(guardian, ward):
        raise ValueError("You are not a registered guardian of this user.")
    revoked = ParentalConsent.objects.filter(
        minor=ward, status=ParentalConsent.Status.ACTIVE
    ).update(status=ParentalConsent.Status.REVOKED, revoked_at=timezone.now())
    from apps.messaging.services import remove_user_from_conversations

    remove_user_from_conversations(ward, reason="consent_revoked")
    return revoked
