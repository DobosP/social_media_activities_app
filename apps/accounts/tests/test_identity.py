import pytest

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.identity.providers.dev import DevIdentityProvider
from apps.accounts.identity.providers.eudi import EUDIWalletProvider
from apps.accounts.identity.registry import get_identity_provider
from apps.accounts.models import AgeBand, User


@pytest.mark.django_db
def test_dev_provider_verifies():
    user = User.objects.create_user(username="p", password="pw")
    result = DevIdentityProvider().verify(user, age_band=AgeBand.UNDER_16)
    assert isinstance(result, AssuranceResult)
    assert result.age_band == AgeBand.UNDER_16
    assert result.parental_consent_required is True


def test_eudi_provider_is_stub():
    with pytest.raises(NotImplementedError):
        EUDIWalletProvider().verify(user=None)


def test_registry_returns_configured_provider():
    provider = get_identity_provider()
    assert provider.name == "dev"  # the default configured in settings
