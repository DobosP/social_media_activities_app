import jwt
import pytest
from cryptography.hazmat.primitives.asymmetric import ec
from django.conf import settings
from rest_framework.test import APIClient

from apps.accounts.identity.base import IdentityVerificationError
from apps.accounts.identity.eudi import issuer
from apps.accounts.identity.eudi.trust import trusted_issuers
from apps.accounts.identity.eudi.verifier import verify_age_presentation
from apps.accounts.models import User

AUD = "rp-test"


def _trusted():
    return {issuer.SANDBOX_ISSUER: issuer.sandbox_public_key_pem()}


def _token(**kw):
    kw.setdefault("audience", AUD)
    kw.setdefault("nonce", "n1")
    return issuer.issue_age_credential(**kw)


def _holder_keypair():
    """Generate an ES256 holder key and its public JWK (the credential `cnf.jwk`)."""
    private_key = ec.generate_private_key(ec.SECP256R1())
    public_jwk = jwt.algorithms.ECAlgorithm.to_jwk(private_key.public_key(), as_dict=True)
    return private_key, public_jwk


def _kb_proof(private_key, *, audience=AUD, nonce="n1"):
    """A holder-signed key-binding JWT (proof-of-possession over audience + nonce)."""
    return jwt.encode(
        {"aud": audience, "nonce": nonce},
        private_key,
        algorithm="ES256",
        headers={"typ": "kb+jwt"},
    )


# --- verifier: real signature / binding checks ---


def test_verifier_accepts_valid_credential():
    claims = verify_age_presentation(
        _token(age_over_16=True, age_over_18=True),
        nonce="n1",
        audience=AUD,
        trusted_issuers=_trusted(),
    )
    assert claims["age_over_18"] is True


def test_verifier_rejects_tampered_signature():
    token = _token(age_over_16=True, age_over_18=True)
    tampered = token[:-4] + ("aaaa" if not token.endswith("aaaa") else "bbbb")
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(tampered, nonce="n1", audience=AUD, trusted_issuers=_trusted())


def test_verifier_rejects_untrusted_issuer():
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            _token(age_over_16=True, age_over_18=True),
            nonce="n1",
            audience=AUD,
            trusted_issuers={},
        )


def test_verifier_rejects_wrong_nonce():
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            _token(age_over_16=True, age_over_18=True),
            nonce="other",
            audience=AUD,
            trusted_issuers=_trusted(),
        )


def test_verifier_rejects_wrong_audience():
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            _token(audience="someone-else", age_over_16=True, age_over_18=True),
            nonce="n1",
            audience=AUD,
            trusted_issuers=_trusted(),
        )


def test_verifier_rejects_expired():
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            _token(age_over_16=True, age_over_18=True, ttl=-120),  # beyond the 30s leeway
            nonce="n1",
            audience=AUD,
            trusted_issuers=_trusted(),
        )


def test_sandbox_issuer_only_trusted_in_sandbox(settings):
    settings.EUDI_SANDBOX = False
    settings.EUDI_TRUSTED_ISSUERS = {}
    assert issuer.SANDBOX_ISSUER not in trusted_issuers()
    settings.EUDI_SANDBOX = True
    assert issuer.SANDBOX_ISSUER in trusted_issuers()


# --- holder binding (proof-of-possession, anti credential-transfer) ---


def test_verifier_without_proof_reports_unverified_holder():
    claims = verify_age_presentation(
        _token(age_over_16=True, age_over_18=True),
        nonce="n1",
        audience=AUD,
        trusted_issuers=_trusted(),
    )
    assert claims["holder_proof"] == "unverified"


def test_verifier_binds_subject_to_expected_holder_without_proof():
    # Even without a key-binding proof, a verified credential cannot be attributed to a
    # different account than the one its subject names.
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            _token(age_over_16=True, age_over_18=True, subject="someone-else"),
            nonce="n1",
            audience=AUD,
            trusted_issuers=_trusted(),
            expected_holder_id="holder-123",
        )


def test_verifier_accepts_valid_holder_binding_proof():
    private_key, public_jwk = _holder_keypair()
    token = _token(
        age_over_16=True,
        age_over_18=True,
        subject="holder-123",
        extra_claims={"cnf": {"jwk": public_jwk}},
    )
    claims = verify_age_presentation(
        token,
        nonce="n1",
        audience=AUD,
        trusted_issuers=_trusted(),
        holder_binding_proof=_kb_proof(private_key),
        expected_holder_id="holder-123",
    )
    assert claims["holder_proof"] == "verified"


def test_verifier_rejects_holder_proof_from_wrong_key():
    # Credential is bound to one holder key; an attacker presents a proof signed by a
    # different key (a lifted credential) -> rejected.
    _, public_jwk = _holder_keypair()
    attacker_key, _ = _holder_keypair()
    token = _token(
        age_over_16=True,
        age_over_18=True,
        subject="holder-123",
        extra_claims={"cnf": {"jwk": public_jwk}},
    )
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            token,
            nonce="n1",
            audience=AUD,
            trusted_issuers=_trusted(),
            holder_binding_proof=_kb_proof(attacker_key),
            expected_holder_id="holder-123",
        )


def test_verifier_rejects_holder_proof_with_replayed_nonce():
    private_key, public_jwk = _holder_keypair()
    token = _token(
        age_over_16=True,
        age_over_18=True,
        subject="holder-123",
        extra_claims={"cnf": {"jwk": public_jwk}},
    )
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            token,
            nonce="n1",
            audience=AUD,
            trusted_issuers=_trusted(),
            # Proof bound to a different nonce than the credential presentation.
            holder_binding_proof=_kb_proof(private_key, nonce="attacker"),
            expected_holder_id="holder-123",
        )


def test_verifier_rejects_holder_proof_when_credential_lacks_cnf():
    private_key, _ = _holder_keypair()
    token = _token(age_over_16=True, age_over_18=True, subject="holder-123")
    with pytest.raises(IdentityVerificationError):
        verify_age_presentation(
            token,
            nonce="n1",
            audience=AUD,
            trusted_issuers=_trusted(),
            holder_binding_proof=_kb_proof(private_key),
            expected_holder_id="holder-123",
        )


# --- end-to-end OpenID4VP API flow ---


@pytest.mark.django_db
def test_verify_age_api_flow_sets_band():
    user = User.objects.create_user(username="eudi", password="pw", display_name="E")
    client = APIClient()
    client.force_authenticate(user)

    started = client.post("/api/accounts/verify-age/start/")
    assert started.status_code == 200
    nonce = started.data["nonce"]
    state = started.data["state"]

    token = issuer.issue_age_credential(
        audience=settings.EUDI_CLIENT_ID,
        nonce=nonce,
        age_over_16=True,
        age_over_18=True,
        subject=str(user.public_id),
    )
    done = client.post("/api/accounts/verify-age/", {"vp_token": token, "state": state})
    assert done.status_code == 200

    user.refresh_from_db()
    assert user.is_identity_verified is True
    assert user.cohort == "adult"
    assert user.age_assurances.filter(provider="eudi").exists()


@pytest.mark.django_db
def test_verify_age_api_rejects_replayed_nonce():
    user = User.objects.create_user(username="eudi2", password="pw")
    client = APIClient()
    client.force_authenticate(user)
    started = client.post("/api/accounts/verify-age/start/")
    state = started.data["state"]

    # Token bound to a different nonce than the issued one -> replay rejected.
    token = issuer.issue_age_credential(
        audience=settings.EUDI_CLIENT_ID, nonce="attacker", age_over_16=True, age_over_18=True
    )
    resp = client.post("/api/accounts/verify-age/", {"vp_token": token, "state": state})
    assert resp.status_code == 400
    user.refresh_from_db()
    assert user.is_identity_verified is False


@pytest.mark.django_db
def test_verify_age_api_nonce_is_single_use():
    """W2-9: even a validly-signed presentation cannot be redeemed twice. A captured
    state+vp_token replayed against a new /start nonce must be rejected."""
    from apps.accounts.models import ConsumedAgeNonce

    user = User.objects.create_user(username="eudi3", password="pw")
    client = APIClient()
    client.force_authenticate(user)

    started = client.post("/api/accounts/verify-age/start/")
    nonce = started.data["nonce"]
    state = started.data["state"]
    token = issuer.issue_age_credential(
        audience=settings.EUDI_CLIENT_ID,
        nonce=nonce,
        age_over_16=True,
        age_over_18=True,
        subject=str(user.public_id),
    )

    first = client.post("/api/accounts/verify-age/", {"vp_token": token, "state": state})
    assert first.status_code == 200
    assert ConsumedAgeNonce.objects.filter(nonce=nonce).count() == 1

    # Same (still-unexpired) state + token replayed -> rejected as already used.
    replay = client.post("/api/accounts/verify-age/", {"vp_token": token, "state": state})
    assert replay.status_code == 400
    assert replay.data["detail"] == "This verification has already been used."
    assert ConsumedAgeNonce.objects.filter(nonce=nonce).count() == 1
