"""Resolve the set of trusted age-credential issuers.

Production: the EU trust list (``EUDI_TRUSTED_ISSUERS``: {issuer_id: PEM public key}).
Sandbox: additionally trust the local test issuer so the demo/API flow verifies for real.
"""

from django.conf import settings


def trusted_issuers() -> dict:
    issuers = dict(getattr(settings, "EUDI_TRUSTED_ISSUERS", {}))
    if getattr(settings, "EUDI_SANDBOX", False):
        from .issuer import SANDBOX_ISSUER, sandbox_public_key_pem

        issuers[SANDBOX_ISSUER] = sandbox_public_key_pem()
    return issuers
