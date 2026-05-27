import pytest

from apps.accounts.identity.base import AssuranceResult, IdentityVerificationError
from apps.accounts.identity.providers.dev import DevIdentityProvider
from apps.accounts.identity.providers.eudi import EUDIWalletProvider
from apps.accounts.identity.registry import get_identity_provider
from apps.accounts.models import AgeBand, User


def _presentation(*, over_16, over_18, **extra):
    return {"verified": True, "age_over_16": over_16, "age_over_18": over_18, **extra}


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
    assert isinstance(result, AssuranceResult)
    assert result.age_band == expected
    assert result.verified is True
    assert result.provider == "eudi"


def test_eudi_result_carries_only_age_band_no_pii():
    result = EUDIWalletProvider().verify(
        user=None,
        presentation=_presentation(over_16=True, over_18=True, expires_at="2027-01-01T00:00:00"),
    )
    assert result.age_band == AgeBand.ADULT
    assert result.expires_at is not None
    assert set(result.raw) == {"age_over_16", "age_over_18", "format"}


def test_eudi_requires_a_presentation():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(user=None)


def test_eudi_rejects_unverified_presentation():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(
            user=None,
            presentation={"age_over_16": True, "age_over_18": True},
        )


def test_eudi_rejects_identifying_claims():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(
            user=None,
            presentation=_presentation(over_16=True, over_18=True, birth_date="2000-01-01"),
        )


def test_eudi_requires_both_age_thresholds():
    with pytest.raises(IdentityVerificationError):
        EUDIWalletProvider().verify(user=None, presentation={"verified": True, "age_over_18": True})


def test_registry_returns_configured_provider():
    provider = get_identity_provider()
    assert provider.name == "dev"  # the default configured in settings
