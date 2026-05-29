from django.db import transaction
from django.utils import timezone

from .models import (
    COHORT_BY_AGE_BAND,
    AgeAssurance,
    Cohort,
    GuardianRelationship,
    ParentalConsent,
    User,
)


def assign_cohort(age_band: str) -> str:
    return COHORT_BY_AGE_BAND.get(age_band, Cohort.UNASSIGNED)


def apply_assurance(user: User, result) -> AgeAssurance:
    """Persist an assurance result onto the user and record it. Does NOT by itself
    grant participation for minors — that still requires valid parental consent."""
    user.age_band = result.age_band
    user.recompute_cohort()
    user.is_identity_verified = bool(result.verified)
    user.identity_verified_at = timezone.now() if result.verified else None
    user.save(update_fields=["age_band", "cohort", "is_identity_verified", "identity_verified_at"])
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


def can_participate(user: User) -> bool:
    """The gate D3/D4 will use: identity-verified, and (if under 16) a valid parental
    consent on file."""
    if not user.is_identity_verified:
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


@transaction.atomic
def revoke_guardian(guardian: User, ward: User) -> None:
    GuardianRelationship.objects.filter(guardian=guardian, ward=ward).update(
        status=GuardianRelationship.Status.REVOKED
    )
    # End any messaging observer presence the (now-revoked) guardianship justified, so an
    # adult cannot keep reading a child's E2EE conversation after the relationship ends.
    from apps.messaging.services import drop_guardian_observers_for

    drop_guardian_observers_for(guardian, ward)


def is_guardian_of(guardian: User, ward: User) -> bool:
    return GuardianRelationship.objects.filter(
        guardian=guardian, ward=ward, status=GuardianRelationship.Status.ACTIVE
    ).exists()


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
