from datetime import datetime

from apps.accounts.identity.base import (
    AssuranceResult,
    IdentityProvider,
    IdentityVerificationError,
)
from apps.accounts.models import AgeBand

# Zero-knowledge "age over N" claims the wallet presents. These are booleans, never a
# birthdate. Romania's digital age of majority is 16 and adulthood is 18, so those are
# the two thresholds our presentation request asks the wallet to prove.
AGE_OVER_16 = "age_over_16"
AGE_OVER_18 = "age_over_18"

# Claims that would identify the holder. We never read or store them, so this provider
# can only ever yield an age band (data minimisation — see docs/COMPLIANCE.md).
_PII_CLAIMS = frozenset(
    {
        "given_name",
        "family_name",
        "name",
        "birth_date",
        "birthdate",
        "date_of_birth",
        "age_in_years",
        "document_number",
        "personal_administrative_number",
        "portrait",
        "resident_address",
        "address",
        "nationality",
    }
)


class EUDIWalletProvider(IdentityProvider):
    """EU Digital Identity (EUDI) Wallet + EU age-verification app.

    A presentation request for an age-band proof is initiated out of band; the wallet
    responds with a verifiable presentation carrying zero-knowledge "over 16 / over 18"
    claims (no name or birthdate). `verify` validates that presentation and returns an
    AssuranceResult carrying ONLY the proven band.

    The cryptographic verification of the presentation (OpenID4VP / ISO 18013-5 against
    the EU trust list) is the `_verify_presentation` seam: pending Romania's national
    wallet rollout (due Dec 2026) the default enforces that an upstream verifier has
    already attested the presentation, and the real trust-anchor check plugs in there.
    See docs/COMPLIANCE.md.
    """

    name = "eudi"

    def verify(self, user, *, presentation: dict | None = None, **kwargs) -> AssuranceResult:
        if not isinstance(presentation, dict) or not presentation:
            raise IdentityVerificationError(
                "EUDI verification requires a wallet `presentation` dict; got none."
            )

        self._verify_presentation(presentation)

        claims = presentation.get("claims", presentation)
        age_band = self._age_band_from_claims(claims)
        return AssuranceResult(
            age_band=age_band,
            verified=True,
            provider=self.name,
            method=presentation.get("method", "openid4vp_age_over"),
            expires_at=self._parse_expiry(presentation.get("expires_at")),
            raw={
                AGE_OVER_16: bool(claims.get(AGE_OVER_16)),
                AGE_OVER_18: bool(claims.get(AGE_OVER_18)),
                "format": presentation.get("format", ""),
            },
        )

    def _verify_presentation(self, presentation: dict) -> None:
        """Cryptographically verify the wallet presentation.

        Production override: perform OpenID4VP / ISO 18013-5 verification of the
        presentation against the EU trust list and the expected nonce/audience. Until
        Romania's wallet ships there is no trust anchor to verify against, so we require
        the presentation to be flagged as already verified by an upstream component and
        refuse anything that is not.
        """
        if presentation.get("verified") is not True:
            raise IdentityVerificationError(
                "Wallet presentation is not cryptographically verified."
            )

    def _age_band_from_claims(self, claims: dict) -> str:
        for key in _PII_CLAIMS:
            if key in claims:
                raise IdentityVerificationError(
                    f"Presentation carries identifying claim {key!r}; age-band proofs "
                    "must disclose only over-age booleans."
                )

        over_16 = claims.get(AGE_OVER_16)
        over_18 = claims.get(AGE_OVER_18)
        if not isinstance(over_16, bool) or not isinstance(over_18, bool):
            raise IdentityVerificationError(
                f"Presentation must prove both {AGE_OVER_16!r} and {AGE_OVER_18!r} as booleans."
            )
        if over_18 and not over_16:
            raise IdentityVerificationError("Contradictory age claims: over 18 but not over 16.")

        if over_18:
            return AgeBand.ADULT
        if over_16:
            return AgeBand.AGE_16_17
        return AgeBand.UNDER_16

    def _parse_expiry(self, value) -> datetime | None:
        if value is None:
            return None
        if isinstance(value, datetime):
            return value
        try:
            return datetime.fromisoformat(value)
        except (TypeError, ValueError) as exc:
            raise IdentityVerificationError(f"Invalid expires_at: {value!r}.") from exc
