from apps.accounts.identity.base import AssuranceResult, IdentityProvider


class EUDIWalletProvider(IdentityProvider):
    """FUTURE: EU Digital Identity (EUDI) Wallet + EU age-verification app.

    Real integration: initiate a presentation request for an age-band proof
    (zero-knowledge "over 13/16/18"), verify the wallet's response/credential, and
    return an AssuranceResult carrying ONLY the proven band (no name/birthdate).
    Pending Romania's national wallet rollout (due Dec 2026). See docs/COMPLIANCE.md.
    """

    name = "eudi"

    def verify(self, user, **kwargs) -> AssuranceResult:
        raise NotImplementedError(
            "EUDI Wallet integration is pending the national wallet rollout; "
            "see the class docstring and docs/COMPLIANCE.md."
        )
