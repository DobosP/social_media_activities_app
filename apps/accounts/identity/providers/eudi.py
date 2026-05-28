import datetime as dt

from django.conf import settings

from apps.accounts.identity.base import (
    AssuranceResult,
    IdentityProvider,
    IdentityVerificationError,
)
from apps.accounts.identity.eudi.trust import trusted_issuers
from apps.accounts.identity.eudi.verifier import verify_age_presentation
from apps.accounts.models import AgeBand

# Zero-knowledge "age over N" claims the wallet presents. These are booleans, never a
# birthdate. Romania's digital age of majority is 16 and adulthood is 18, so those are
# the two thresholds our presentation request asks the wallet to prove.
AGE_OVER_16 = "age_over_16"
AGE_OVER_18 = "age_over_18"

# How long a verified age proof is trusted before re-verification is required.
ASSURANCE_VALIDITY_DAYS = 365

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

    A presentation request for an age-band proof is initiated out of band (OpenID4VP); the
    wallet responds with a verifiable presentation carrying zero-knowledge "over 16 / over
    18" claims (no name or birthdate). `verify` **cryptographically verifies** that
    presentation — ES256 signature against the trusted-issuer list, audience + nonce/replay
    binding and expiry — and returns an AssuranceResult carrying ONLY the proven band.

    The trust anchor is configurable (`EUDI_TRUSTED_ISSUERS`, the EU trust list in
    production; a local test issuer in sandbox mode). The credential-format parsing
    (JWT-VC here; SD-JWT VC / ISO mdoc in production) is isolated in
    `apps.accounts.identity.eudi.verifier`. See docs/COMPLIANCE.md.
    """

    name = "eudi"

    def verify(self, user, *, presentation: dict | None = None, **kwargs) -> AssuranceResult:
        if not isinstance(presentation, dict) or not presentation:
            raise IdentityVerificationError(
                "EUDI verification requires a wallet `presentation` dict; got none."
            )

        claims = self._verify_presentation(presentation)
        age_band = self._age_band_from_claims(claims)
        return AssuranceResult(
            age_band=age_band,
            verified=True,
            provider=self.name,
            method=presentation.get("method", "openid4vp"),
            expires_at=self._expiry(claims),
            raw={
                AGE_OVER_16: bool(claims.get(AGE_OVER_16)),
                AGE_OVER_18: bool(claims.get(AGE_OVER_18)),
                "format": presentation.get("format", "jwt_vc"),
            },
        )

    def _verify_presentation(self, presentation: dict) -> dict:
        """Cryptographically verify the wallet presentation and return its claims.

        Verifies the signed ``vp_token`` (OpenID4VP) against the trusted-issuer list, with
        audience + nonce binding and expiry. The trust-anchor check is the only part that
        differs from production (sandbox issuer vs the EU trust list)."""
        token = presentation.get("vp_token") or presentation.get("credential")
        if not token:
            raise IdentityVerificationError("Wallet presentation must carry a signed `vp_token`.")
        return verify_age_presentation(
            token,
            nonce=presentation.get("nonce"),
            audience=presentation.get("audience") or settings.EUDI_CLIENT_ID,
            trusted_issuers=trusted_issuers(),
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

    def _expiry(self, claims: dict) -> dt.datetime:
        exp = claims.get("exp")
        if isinstance(exp, int | float):
            return dt.datetime.fromtimestamp(exp, tz=dt.UTC)
        return dt.datetime.now(tz=dt.UTC) + dt.timedelta(days=ASSURANCE_VALIDITY_DAYS)
