from django.utils import timezone

from .models import COHORT_BY_AGE_BAND, AgeAssurance, Cohort, User


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
