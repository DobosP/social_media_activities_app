"""Cryptographically verify an EUDI age attestation presented over OpenID4VP.

The attestation is a signed JWT (JWT-format verifiable credential) carrying
privacy-preserving over-threshold booleans (``age_over_16`` / ``age_over_18``) - never a
birthdate. We verify the issuer's ES256 signature against the trusted-issuer set, bind it
to our audience + nonce (replay protection) and expiry, and return the verified claims for
the provider to map to an age band.

Holder binding (anti-transfer): a credential carries the holder's public key in a
``cnf`` confirmation claim (RFC 7800). When the wallet also presents a key-binding proof
(a holder-signed JWT over our audience + nonce), :func:`verify_age_presentation` proves
that the *presenter* controls that key — i.e. the credential was not lifted from another
holder and replayed. The proof's subject is then bound to a stable per-user holder id so
the same wallet/credential cannot be re-used to assure a different account. When no
key-binding proof is supplied the credential's signature, audience, nonce and expiry are
still fully verified (current behaviour); only proof-of-possession of the holder key is
not established — see the ``holder_proof`` note in the return value.

Production note: the EU wallet also uses SD-JWT VC and ISO mdoc formats; this is the seam
where that parsing/verification plugs in (the surrounding protocol/trust handling is the
same). The key-binding check here mirrors SD-JWT VC's KB-JWT and mdoc's device signature.
"""

import jwt

from apps.accounts.identity.base import IdentityVerificationError


def verify_age_presentation(
    vp_token: str,
    *,
    nonce: str,
    audience: str,
    trusted_issuers: dict,
    leeway: int = 30,
    holder_binding_proof: str | None = None,
    expected_holder_id: str | None = None,
) -> dict:
    """Return the verified credential claims, or raise IdentityVerificationError.

    ``holder_binding_proof`` is an optional holder-signed key-binding JWT proving the
    presenter controls the key the credential is bound to (its ``cnf`` claim). When given,
    its signature is verified against that key and it must be bound to the same ``audience``
    and ``nonce``; ``expected_holder_id`` (a stable per-user holder id) is enforced against
    the credential subject so a credential cannot be replayed to assure another account.
    When omitted, the credential is still fully verified but proof-of-possession is not
    established (``holder_proof`` is reported as ``"unverified"`` in the returned claims).
    """
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

    _verify_holder_binding(
        claims,
        holder_binding_proof=holder_binding_proof,
        expected_holder_id=expected_holder_id,
        nonce=nonce,
        audience=audience,
        leeway=leeway,
    )
    return claims


def _verify_holder_binding(
    claims: dict,
    *,
    holder_binding_proof: str | None,
    expected_holder_id: str | None,
    nonce: str,
    audience: str,
    leeway: int,
) -> None:
    """Verify proof-of-possession of the credential holder key, when a proof is presented.

    Records the outcome on ``claims["holder_proof"]`` ("verified" / "unverified"). With a
    proof: the credential's ``cnf`` confirmation key (RFC 7800) must verify the holder-
    signed key-binding JWT over our audience + nonce, and the credential ``sub`` must equal
    ``expected_holder_id`` (the stable per-user holder id). Without a proof, the credential
    remains issuer-verified but holder binding is left unproven (documented limitation).
    """
    if not holder_binding_proof:
        # No key-binding proof: keep current behaviour. Still bind sub to the expected
        # holder id when one is supplied, so a verified credential cannot silently be
        # attributed to a different user even absent proof-of-possession.
        _enforce_subject(claims, expected_holder_id)
        claims["holder_proof"] = "unverified"
        return

    confirmation = claims.get("cnf")
    holder_key = confirmation.get("jwk") if isinstance(confirmation, dict) else None
    if not holder_key:
        raise IdentityVerificationError(
            "Key-binding proof supplied but the credential carries no holder key (cnf.jwk)."
        )
    try:
        holder_pubkey = jwt.PyJWK.from_dict(holder_key).key
    except (jwt.InvalidKeyError, jwt.PyJWKError, KeyError, ValueError) as exc:
        raise IdentityVerificationError(f"Unusable holder confirmation key: {exc}") from exc

    try:
        proof = jwt.decode(
            holder_binding_proof,
            holder_pubkey,
            algorithms=["ES256"],
            audience=audience,
            options={"require": ["aud", "nonce"]},
            leeway=leeway,
        )
    except jwt.InvalidTokenError as exc:
        raise IdentityVerificationError(
            f"Holder key-binding proof verification failed: {exc}"
        ) from exc

    if proof.get("nonce") != nonce:
        raise IdentityVerificationError(
            "Holder key-binding proof nonce mismatch (possible replay)."
        )

    _enforce_subject(claims, expected_holder_id)
    claims["holder_proof"] = "verified"


def _enforce_subject(claims: dict, expected_holder_id: str | None) -> None:
    """Bind the credential subject to the stable per-user holder id, if one is expected."""
    if expected_holder_id is None:
        return
    if claims.get("sub") != expected_holder_id:
        raise IdentityVerificationError(
            "Credential is bound to a different holder than this account."
        )
