import pytest
from django.conf import settings

from apps.accounts.identity.base import AssuranceResult, IdentityVerificationError
from apps.accounts.identity.eudi import issuer
from apps.accounts.identity.providers.dev import DevIdentityProvider
from apps.accounts.identity.providers.eudi import EUDIWalletProvider
from apps.accounts.identity.registry import get_identity_provider
from apps.accounts.models import AgeBand, User


def _presentation(*, over_16=None, over_18=None, nonce="n1", extra_claims=None):
    """A cryptographically-signed wallet presentation from the sandbox issuer."""
    token = issuer.issue_age_credential(
        audience=settings.EUDI_CLIENT_ID,
        nonce=nonce,
        age_over_16=over_16,
        age_over_18=over_18,
        extra_claims=extra_claims,
    )
    return {"vp_token": token, "nonce": nonce, "audience": settings.EUDI_CLIENT_ID}


@pytest.mark.django_db
def test_dev_provider_verifies():
    user = User.objects.create_user(username="p", password="pw")
    result = DevIdentityProvider().verify(user, age_band=AgeBand.UNDER_16)
    assert isinstance(result, AssuranceResult)
    assert result.age_band == AgeBand.UNDER_16
    assert result.parental_consent_required is True


@pytest.mark.parametrize(
    ("over_16", "over_18", "expected"),
    [
        (True, True, AgeBand.ADULT),
        (True, False, AgeBand.AGE_16_17),
        (False, False, AgeBand.UNDER_16),
    ],
)
def test_eudi_maps_age_over_claims_to_band(over_16, over_18, expected):
    result = EUDIWalletProvider().verify(
        user=None, presentation=_presentation(over_16=over_16, over_18=over_18)
    )
    assert result.age_band == expected
    assert result.verified is True
    assert result.provider == "eudi"


def test_eudi_result_carries_only_age_band_no_pii():
    result = EUDIWalletProvider().verify(
        user=None, presentation=_presentation(over_16=True, over_18=True)
    )
    assert result.age_band == AgeBand.ADULT
    assert result.expires_at is not None
    assert set(result.raw) == {"age_over_16", "age_over_18", "format"}


def test_eudi_requires_a_presentation():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(user=None)


def test_eudi_rejects_presentation_without_signed_token():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(
            user=None, presentation={"age_over_16": True, "age_over_18": True}
        )


def test_eudi_rejects_identifying_claims():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(
            user=None,
            presentation=_presentation(
                over_16=True, over_18=True, extra_claims={"birth_date": "2000-01-01"}
            ),
        )


def test_eudi_requires_both_age_thresholds():
    # A signed token proving only over-18 (age_over_16 omitted) is rejected.
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(user=None, presentation=_presentation(over_18=True))


def test_registry_returns_configured_provider():
    provider = get_identity_provider()
    assert provider.name == "dev"  # the default configured in settings
