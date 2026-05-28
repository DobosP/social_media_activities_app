"""Cryptographically verify an EUDI age attestation presented over OpenID4VP.

The attestation is a signed JWT (JWT-format verifiable credential) carrying
privacy-preserving over-threshold booleans (``age_over_16`` / ``age_over_18``) - never a
birthdate. We verify the issuer's ES256 signature against the trusted-issuer set, bind it
to our audience + nonce (replay protection) and expiry, and return the verified claims for
the provider to map to an age band.

Production note: the EU wallet also uses SD-JWT VC and ISO mdoc formats; this is the seam
where that parsing/verification plugs in (the surrounding protocol/trust handling is the
same).
"""

import jwt

from apps.accounts.identity.base import IdentityVerificationError


def verify_age_presentation(
    vp_token: str, *, nonce: str, audience: str, trusted_issuers: dict, leeway: int = 30
) -> dict:
    """Return the verified credential claims, or raise IdentityVerificationError."""
    if not vp_token:
        raise IdentityVerificationError("Missing wallet presentation token.")
    try:
        unverified = jwt.decode(vp_token, options={"verify_signature": False})
    except jwt.InvalidTokenError as exc:
        raise IdentityVerificationError(f"Malformed credential: {exc}") from exc

    issuer = unverified.get("iss")
    key = trusted_issuers.get(issuer) if issuer else None
    if key is None:
        raise IdentityVerificationError("Credential issuer is not in the trust list.")

    try:
        claims = jwt.decode(
            vp_token,
            key,
            algorithms=["ES256"],
            audience=audience,
            options={"require": ["exp", "iss", "aud"]},
            leeway=leeway,
        )
    except jwt.InvalidTokenError as exc:
        raise IdentityVerificationError(f"Credential verification failed: {exc}") from exc

    if not nonce or claims.get("nonce") != nonce:
        raise IdentityVerificationError("Nonce mismatch (possible replay).")

    return claims
