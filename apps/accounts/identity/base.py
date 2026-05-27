from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from datetime import datetime

from apps.accounts.models import AgeBand


@dataclass
class AssuranceResult:
    """What an identity provider returns: a proven age band and verification status,
    NOT raw identity (no name/birthdate)."""

    age_band: str
    verified: bool = True
    provider: str = ""
    method: str = ""
    expires_at: datetime | None = None
    raw: dict = field(default_factory=dict)

    @property
    def parental_consent_required(self) -> bool:
        return self.age_band == AgeBand.UNDER_16


class IdentityProvider(ABC):
    """Wraps an external age-assurance mechanism (the EU EUDI Wallet /
    age-verification app) behind a stable interface, so the rest of the app depends
    only on the result, never on a specific scheme. See docs/COMPLIANCE.md."""

    name: str

    @abstractmethod
    def verify(self, user, **kwargs) -> AssuranceResult:
        raise NotImplementedError
