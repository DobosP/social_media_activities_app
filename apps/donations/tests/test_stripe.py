from unittest.mock import MagicMock, patch

import pytest
import requests
from django.core.exceptions import ImproperlyConfigured
from django.test import override_settings

from apps.donations.providers import StripePaymentProvider


@override_settings(
    STRIPE_SECRET_KEY="sk_test_123",
    DONATIONS_SUCCESS_URL="https://x/thanks",
    DONATIONS_CANCEL_URL="https://x/donate",
)
@patch("requests.request")
def test_stripe_creates_checkout_session(mock_req):
    resp = MagicMock()
    resp.status_code = 200
    resp.json.return_value = {"url": "https://checkout.stripe.com/c/abc", "id": "cs_test_1"}
    mock_req.return_value = resp

    intent = StripePaymentProvider().create_intent(500, "EUR", reference="ref-1")

    assert intent.checkout_url == "https://checkout.stripe.com/c/abc"
    assert intent.external_ref == "cs_test_1"
    args, kwargs = mock_req.call_args
    assert args[0] == "POST"
    assert kwargs["auth"] == ("sk_test_123", "")
    assert kwargs["data"]["line_items[0][price_data][unit_amount]"] == 500
    assert kwargs["data"]["line_items[0][price_data][currency]"] == "eur"
    assert kwargs["data"]["client_reference_id"] == "ref-1"
    # The Idempotency-Key makes a retried POST safe (Stripe dedupes it).
    assert kwargs["headers"]["Idempotency-Key"] == "ref-1"


@override_settings(STRIPE_SECRET_KEY="")
def test_stripe_requires_secret_key():
    with pytest.raises(ImproperlyConfigured):
        StripePaymentProvider().create_intent(500, "EUR", reference="r")


@pytest.mark.django_db
@override_settings(
    DONATIONS_PROVIDER="apps.donations.providers.StripePaymentProvider",
    STRIPE_SECRET_KEY="sk_test_123",
    DONATIONS_SUCCESS_URL="https://x/t",
    DONATIONS_CANCEL_URL="https://x/c",
)
@patch("apps.ops.resilience.time.sleep", lambda *a, **k: None)  # no real backoff sleeps
@patch("requests.request")
def test_start_donation_maps_provider_failure_to_donation_error(mock_req):
    # A transient provider failure becomes a clean DonationError (the view maps it to 400), never a
    # 500; and no Donation row is created when the intent fails.
    from apps.donations.models import Donation
    from apps.donations.services import DonationError, start_donation

    mock_req.side_effect = requests.ConnectionError("provider down")
    with pytest.raises(DonationError):
        start_donation(None, 500, "EUR")
    assert Donation.objects.count() == 0
