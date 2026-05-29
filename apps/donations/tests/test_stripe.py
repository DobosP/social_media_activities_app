from unittest.mock import MagicMock, patch

import pytest
from django.core.exceptions import ImproperlyConfigured
from django.test import override_settings

from apps.donations.providers import StripePaymentProvider


@override_settings(
    STRIPE_SECRET_KEY="sk_test_123",
    DONATIONS_SUCCESS_URL="https://x/thanks",
    DONATIONS_CANCEL_URL="https://x/donate",
)
@patch("requests.post")
def test_stripe_creates_checkout_session(mock_post):
    resp = MagicMock()
    resp.json.return_value = {"url": "https://checkout.stripe.com/c/abc", "id": "cs_test_1"}
    resp.raise_for_status.return_value = None
    mock_post.return_value = resp

    intent = StripePaymentProvider().create_intent(500, "EUR", reference="ref-1")

    assert intent.checkout_url == "https://checkout.stripe.com/c/abc"
    assert intent.external_ref == "cs_test_1"
    _, kwargs = mock_post.call_args
    assert kwargs["auth"] == ("sk_test_123", "")
    assert kwargs["data"]["line_items[0][price_data][unit_amount]"] == 500
    assert kwargs["data"]["line_items[0][price_data][currency]"] == "eur"
    assert kwargs["data"]["client_reference_id"] == "ref-1"


@override_settings(STRIPE_SECRET_KEY="")
def test_stripe_requires_secret_key():
    with pytest.raises(ImproperlyConfigured):
        StripePaymentProvider().create_intent(500, "EUR", reference="r")
