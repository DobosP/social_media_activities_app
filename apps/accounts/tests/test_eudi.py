import pytest
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
