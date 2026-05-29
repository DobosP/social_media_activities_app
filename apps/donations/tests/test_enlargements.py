import hashlib
import hmac
import json
import time

import pytest
from django.test import override_settings
from rest_framework.test import APIClient

from apps.donations.models import Donation
from apps.donations.services import completed_total_cents, start_donation

pytestmark = pytest.mark.django_db

DEV = "apps.donations.providers.DevPaymentProvider"
STRIPE = "apps.donations.providers.StripePaymentProvider"


@override_settings(DONATIONS_PROVIDER=DEV)
def test_recurring_flag_persists():
    donation, _ = start_donation(None, 500, recurring=True)
    assert donation.recurring is True


@override_settings(DONATIONS_PROVIDER=DEV)
def test_completed_total_aggregates_only_completed():
    d1, _ = start_donation(None, 500)
    d2, _ = start_donation(None, 1500)
    assert completed_total_cents() == 0
    d1.mark_completed()
    assert completed_total_cents() == 500
    d2.mark_completed()
    assert completed_total_cents() == 2000


@override_settings(DONATIONS_PROVIDER=DEV)
def test_total_endpoint_public():
    d, _ = start_donation(None, 700)
    d.mark_completed()
    resp = APIClient().get("/api/donations/total/")
    assert resp.status_code == 200
    assert resp.json() == {"currency": "EUR", "total_cents": 700}


@override_settings(DONATIONS_PROVIDER=DEV, DONATIONS_WEBHOOK_SECRET="s3cret")
def test_webhook_completes_with_secret():
    donation, _ = start_donation(None, 900)
    client = APIClient()
    # Wrong secret rejected.
    bad = client.post(
        "/api/donations/webhook/", {"external_ref": donation.external_ref}, format="json"
    )
    assert bad.status_code == 403

    ok = client.post(
        "/api/donations/webhook/",
        {"external_ref": donation.external_ref},
        format="json",
        HTTP_X_WEBHOOK_SECRET="s3cret",
    )
    assert ok.status_code == 200
    donation.refresh_from_db()
    assert donation.status == Donation.Status.COMPLETED


@override_settings(DONATIONS_PROVIDER=DEV, DONATIONS_WEBHOOK_SECRET="s3cret")
def test_webhook_unknown_ref_ignored():
    # Authenticated (correct secret) but an unknown reference is a no-op, not an error.
    resp = APIClient().post(
        "/api/donations/webhook/",
        {"external_ref": "nope"},
        format="json",
        HTTP_X_WEBHOOK_SECRET="s3cret",
    )
    assert resp.status_code == 200
    assert resp.json()["status"] == "ignored"


def _stripe_signed(payload: bytes, secret: str):
    ts = int(time.time())
    v1 = hmac.new(secret.encode(), f"{ts}.".encode() + payload, hashlib.sha256).hexdigest()
    return f"t={ts},v1={v1}"


@override_settings(DONATIONS_PROVIDER=STRIPE, STRIPE_WEBHOOK_SECRET="whsec_test")
def test_stripe_webhook_completes_on_signed_event():
    # A real Stripe Event is nested; external_ref is the Checkout Session id.
    donation = Donation.objects.create(
        amount_cents=1200, currency="EUR", provider="stripe", external_ref="cs_test_abc"
    )
    payload = json.dumps(
        {
            "id": "evt_1",
            "type": "checkout.session.completed",
            "data": {"object": {"id": "cs_test_abc"}},
        }
    ).encode()
    resp = APIClient().post(
        "/api/donations/webhook/",
        data=payload,
        content_type="application/json",
        HTTP_STRIPE_SIGNATURE=_stripe_signed(payload, "whsec_test"),
    )
    assert resp.status_code == 200, resp.content
    donation.refresh_from_db()
    assert donation.status == Donation.Status.COMPLETED


@override_settings(DONATIONS_PROVIDER=STRIPE, STRIPE_WEBHOOK_SECRET="whsec_test")
def test_stripe_webhook_rejects_bad_signature():
    donation = Donation.objects.create(
        amount_cents=1200, currency="EUR", provider="stripe", external_ref="cs_test_xyz"
    )
    payload = json.dumps(
        {"type": "checkout.session.completed", "data": {"object": {"id": "cs_test_xyz"}}}
    ).encode()
    resp = APIClient().post(
        "/api/donations/webhook/",
        data=payload,
        content_type="application/json",
        HTTP_STRIPE_SIGNATURE=_stripe_signed(payload, "wrong_secret"),
    )
    assert resp.status_code == 403
    donation.refresh_from_db()
    assert donation.status == Donation.Status.PENDING


@override_settings(DONATIONS_PROVIDER=DEV, DONATIONS_WEBHOOK_SECRET="")
def test_webhook_fails_closed_without_secret():
    # No secret configured anywhere → the webhook must reject every caller (fail-closed),
    # so a pending donation can never be forged complete by an anonymous request.
    donation, _ = start_donation(None, 900)
    resp = APIClient().post(
        "/api/donations/webhook/", {"external_ref": donation.external_ref}, format="json"
    )
    assert resp.status_code == 403
    donation.refresh_from_db()
    assert donation.status == Donation.Status.PENDING
