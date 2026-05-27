from django.conf import settings
from django.core.exceptions import ImproperlyConfigured

from apps.accounts.identity.base import AssuranceResult, IdentityProvider
from apps.accounts.models import AgeBand


class DevIdentityProvider(IdentityProvider):
    """Deterministic stub for local development and tests. NEVER for production.

    `verify` accepts an explicit `age_band` (default ADULT) so the assurance/consent
    flow can be exercised without real EU identity infrastructure.
    """

    name = "dev"

    def __init__(self):
        if not settings.DEBUG and not getattr(settings, "IDENTITY_ALLOW_DEV_PROVIDER", False):
            raise ImproperlyConfigured(
                "DevIdentityProvider is disabled outside DEBUG. Configure a real "
                "IDENTITY_PROVIDER (e.g. the EUDI provider) for production."
            )

    def verify(self, user, *, age_band: str = AgeBand.ADULT, **kwargs) -> AssuranceResult:
        return AssuranceResult(age_band=age_band, verified=True, provider=self.name, method="stub")
