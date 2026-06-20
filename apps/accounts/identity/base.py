from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from datetime import datetime

from apps.accounts.models import AgeBand


class IdentityVerificationError(Exception):
    """Raised when an identity provider cannot establish a trustworthy age band
    (e.g. a missing, malformed, or cryptographically invalid wallet presentation)."""


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
    # Transient holder subject from the wallet presentation, used ONLY to bind one person to
    # one account (see accounts.services.bind_identity). It is deliberately NOT persisted into
    # AgeAssurance.raw — only its HMAC is stored, in IdentityBinding. None for providers (dev
    # stub) that establish no holder key.
    holder_sub: str | None = None

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
