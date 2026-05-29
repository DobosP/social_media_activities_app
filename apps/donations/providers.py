"""Pluggable payment providers. We never touch card data — a provider returns a
checkout URL the donor completes off-platform, plus an opaque reference we store to
reconcile via webhook. Default is a deep-link provider; swap for a real EU-friendly
nonprofit processor (e.g. Stripe/SEPA) in production."""

import uuid
from abc import ABC, abstractmethod
from dataclasses import dataclass

from django.conf import settings
from django.core.exceptions import ImproperlyConfigured
from django.utils.module_loading import import_string


@dataclass(frozen=True)
class PaymentIntent:
    checkout_url: str
    external_ref: str


class PaymentProvider(ABC):
    name = "base"

    @abstractmethod
    def create_intent(
        self, amount_cents: int, currency: str, *, reference: str
    ) -> PaymentIntent: ...


class DeepLinkProvider(PaymentProvider):
    """Builds a deep link to an externally hosted nonprofit checkout page. Stores no
    sensitive data; the reference lets a webhook reconcile completion later."""

    name = "deeplink"

    def create_intent(self, amount_cents: int, currency: str, *, reference: str) -> PaymentIntent:
        base = getattr(settings, "DONATIONS_CHECKOUT_BASE_URL", "") or "https://example.org/donate"
        url = f"{base}?ref={reference}&amount={amount_cents}&currency={currency}"
        return PaymentIntent(checkout_url=url, external_ref=reference)


class DevPaymentProvider(PaymentProvider):
    """Test/dev provider: returns a synthetic intent without any external call."""

    name = "dev"

    def create_intent(self, amount_cents: int, currency: str, *, reference: str) -> PaymentIntent:
        return PaymentIntent(checkout_url=f"dev://checkout/{reference}", external_ref=reference)


class StripePaymentProvider(PaymentProvider):
    """Stripe Checkout — a real EU-friendly processor. We never see card data: a
    Checkout Session is created server-side and the donor completes payment on Stripe's
    hosted page; completion is reconciled via webhook (the external_ref is the session
    id). Activated by setting DONATIONS_PROVIDER to this class + STRIPE_SECRET_KEY.

    Uses the bundled `requests` (no extra dependency); the secret key is the HTTP basic
    username per Stripe's API."""

    name = "stripe"
    API_URL = "https://api.stripe.com/v1/checkout/sessions"

    def create_intent(self, amount_cents: int, currency: str, *, reference: str) -> PaymentIntent:
        import requests

        secret = getattr(settings, "STRIPE_SECRET_KEY", "")
        if not secret:
            raise ImproperlyConfigured("STRIPE_SECRET_KEY must be set to use the Stripe provider.")
        data = {
            "mode": "payment",
            "success_url": getattr(settings, "DONATIONS_SUCCESS_URL", ""),
            "cancel_url": getattr(settings, "DONATIONS_CANCEL_URL", ""),
            "client_reference_id": reference,
            "line_items[0][quantity]": 1,
            "line_items[0][price_data][currency]": currency.lower(),
            "line_items[0][price_data][unit_amount]": amount_cents,
            "line_items[0][price_data][product_data][name]": "Donation",
        }
        response = requests.post(self.API_URL, data=data, auth=(secret, ""), timeout=15)
        response.raise_for_status()
        body = response.json()
        return PaymentIntent(checkout_url=body["url"], external_ref=body["id"])


def new_reference() -> str:
    return uuid.uuid4().hex


def get_payment_provider() -> PaymentProvider:
    return import_string(settings.DONATIONS_PROVIDER)()
