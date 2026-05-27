"""Booking-provider registry.

Maps a provider slug to its adapter class. The built-ins can be extended/overridden
via ``settings.BOOKING_PROVIDERS`` (slug -> dotted import path)."""

from __future__ import annotations

from django.conf import settings
from django.utils.module_loading import import_string

from .providers.base import BookingProvider
from .providers.deeplink import DeepLinkProvider
from .providers.demo_rest import DemoRestProvider

DEFAULT_PROVIDER = "deeplink"

_BUILTIN: dict[str, type[BookingProvider]] = {
    DeepLinkProvider.name: DeepLinkProvider,
    DemoRestProvider.name: DemoRestProvider,
}


def _provider_classes() -> dict[str, type[BookingProvider]]:
    classes = dict(_BUILTIN)
    for slug, path in getattr(settings, "BOOKING_PROVIDERS", {}).items():
        classes[slug] = import_string(path)
    return classes


def get_booking_provider(slug: str | None = None) -> BookingProvider:
    slug = slug or DEFAULT_PROVIDER
    classes = _provider_classes()
    if slug not in classes:
        raise KeyError(f"unknown booking provider: {slug}")
    return classes[slug]()
