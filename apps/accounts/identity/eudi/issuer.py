"""Sandbox age-credential issuer.

Stands in for the (not-yet-live) national EUDI wallet/issuer so the OpenID4VP flow and
tests run end-to-end. It signs a real ES256 JWT credential, which the verifier really
verifies - only the *trust anchor* differs from production (a local test key here, the EU
trust list there). Never trusted unless ``EUDI_SANDBOX`` is on.
"""

import datetime as dt

import jwt
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec
from django.conf import settings

SANDBOX_ISSUER = "https://sandbox.issuer.local/eudi"
VCT = "eu.europa.ec.eudi.age_verification.1"

_private_pem: bytes | None = None
_public_pem: bytes | None = None


def _ensure_keys() -> None:
    global _private_pem, _public_pem
    if _private_pem is not None:
        return
    configured = getattr(settings, "EUDI_SANDBOX_ISSUER_KEY_PEM", "")
    if configured:
        _private_pem = configured.encode() if isinstance(configured, str) else configured
        private_key = serialization.load_pem_private_key(_private_pem, password=None)
    else:
        private_key = ec.generate_private_key(ec.SECP256R1())
        _private_pem = private_key.private_bytes(
            serialization.Encoding.PEM,
            serialization.PrivateFormat.PKCS8,
            serialization.NoEncryption(),
        )
    _public_pem = private_key.public_key().public_bytes(
        serialization.Encoding.PEM, serialization.PublicFormat.SubjectPublicKeyInfo
    )


def sandbox_public_key_pem() -> str:
    _ensure_keys()
    return _public_pem.decode()


def issue_age_credential(
    *,
    audience: str,
    nonce: str,
    age_over_16: bool | None = None,
    age_over_18: bool | None = None,
    subject: str = "sandbox-holder",
    ttl: int = 600,
    extra_claims: dict | None = None,
) -> str:
    _ensure_keys()
    now = dt.datetime.now(dt.UTC)
    claims = {
        "iss": SANDBOX_ISSUER,
        "sub": subject,
        "aud": audience,
        "nonce": nonce,
        "iat": now,
        "exp": now + dt.timedelta(seconds=ttl),
        "vct": VCT,
    }
    # Omit a threshold to model an incomplete attestation (each is a boolean when present).
    if age_over_16 is not None:
        claims["age_over_16"] = bool(age_over_16)
    if age_over_18 is not None:
        claims["age_over_18"] = bool(age_over_18)
    if extra_claims:
        claims.update(extra_claims)
    return jwt.encode(claims, _private_pem, algorithm="ES256")
