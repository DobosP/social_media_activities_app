import pytest
from rest_framework.test import APIClient

from apps.messaging import services
from apps.messaging.models import KeyVerification

from .conftest import PUBLIC_JWK

pytestmark = pytest.mark.django_db


def client_for(user):
    client = APIClient()
    client.force_authenticate(user=user)
    return client


# --- fingerprint derivation ---
def test_key_fingerprint_is_deterministic_and_key_specific():
    fp1 = services.key_fingerprint(PUBLIC_JWK)
    fp2 = services.key_fingerprint(dict(reversed(list(PUBLIC_JWK.items()))))  # key order irrelevant
    assert fp1 == fp2
    assert fp1 != services.key_fingerprint({**PUBLIC_JWK, "x": "ZZZZ"})
    assert len(fp1) == 32


# --- recording a verification ---
def test_record_verification_then_status_verified(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    fp = services.key_fingerprint(PUBLIC_JWK)
    services.record_key_verification(adult_a, adult_b, fp)
    status = services.verification_status(adult_a, adult_b)
    assert status == {"fingerprint": fp, "verified": True}


def test_verify_rejects_wrong_fingerprint(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    with pytest.raises(services.MessagingError):
        services.record_key_verification(adult_a, adult_b, "deadbeef")


def test_verify_requires_a_key(adult_a, adult_b):
    with pytest.raises(services.MessagingError):
        services.record_key_verification(adult_a, adult_b, "anything")


def test_cannot_verify_across_cohort(adult_a, child):
    services.register_public_key(child, PUBLIC_JWK)
    fp = services.key_fingerprint(PUBLIC_JWK)
    with pytest.raises(services.MessagingError):
        services.record_key_verification(adult_a, child, fp)


def test_key_rotation_invalidates_prior_verification(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    services.record_key_verification(adult_a, adult_b, services.key_fingerprint(PUBLIC_JWK))
    assert services.verification_status(adult_a, adult_b)["verified"] is True
    # adult_b rotates to a new key -> the old verification no longer matches.
    rotated = {**PUBLIC_JWK, "x": "Uk9UQVRFRA"}
    services.register_public_key(adult_b, rotated)
    status = services.verification_status(adult_a, adult_b)
    assert status["verified"] is False
    assert status["fingerprint"] == services.key_fingerprint(rotated)


# --- API ---
def test_user_key_endpoint_includes_fingerprint_and_verified(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    resp = client_for(adult_a).get(f"/api/messaging/keys/{adult_b.username}/")
    assert resp.status_code == 200
    assert resp.data["fingerprint"] == services.key_fingerprint(PUBLIC_JWK)
    assert resp.data["verified"] is False


def test_verify_endpoint_records_and_reflects(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    fp = services.key_fingerprint(PUBLIC_JWK)
    resp = client_for(adult_a).post(
        "/api/messaging/verify/", {"username": adult_b.username, "fingerprint": fp}, format="json"
    )
    assert resp.status_code == 200
    assert resp.data["verified"] is True
    assert KeyVerification.objects.filter(verifier=adult_a, subject=adult_b).exists()
    # And the key endpoint now reports it verified for adult_a.
    assert (
        client_for(adult_a).get(f"/api/messaging/keys/{adult_b.username}/").data["verified"] is True
    )


def test_verify_endpoint_rejects_bad_fingerprint(adult_a, adult_b):
    services.register_public_key(adult_b, PUBLIC_JWK)
    resp = client_for(adult_a).post(
        "/api/messaging/verify/",
        {"username": adult_b.username, "fingerprint": "nope"},
        format="json",
    )
    assert resp.status_code == 400
