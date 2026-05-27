"""Pluggable payment providers. We never touch card data — a provider returns a
checkout URL the donor completes off-platform, plus an opaque reference we store to
reconcile via webhook. Default is a deep-link provider; swap for a real EU-friendly
nonprofit processor (e.g. Stripe/SEPA) in production."""

import uuid
from abc import ABC, abstractmethod
from dataclasses import dataclass

from django.conf import settings
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


def new_reference() -> str:
    return uuid.uuid4().hex


def get_payment_provider() -> PaymentProvider:
    return import_string(settings.DONATIONS_PROVIDER)()
