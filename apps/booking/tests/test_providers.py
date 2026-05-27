from datetime import datetime

import pytest

from apps.booking.providers.base import BookingError, BookingNotSupported
from apps.booking.providers.deeplink import DeepLinkProvider
from apps.booking.providers.demo_rest import DemoRestProvider
from apps.booking.registry import get_booking_provider


def test_registry_returns_builtins():
    assert isinstance(get_booking_provider("deeplink"), DeepLinkProvider)
    assert isinstance(get_booking_provider(), DeepLinkProvider)  # default
    assert isinstance(get_booking_provider("demo_rest"), DemoRestProvider)


def test_registry_unknown_raises():
    with pytest.raises(KeyError):
        get_booking_provider("nope")


def test_deeplink_cannot_create():
    with pytest.raises(BookingNotSupported):
        DeepLinkProvider().create_booking(
            place_ref="1", start=datetime(2026, 1, 1), end=None, party_size=1, user_ref="u"
        )


def test_demo_rest_create_and_cancel(monkeypatch, settings):
    settings.BOOKING_DEMO_BASE_URL = "https://api.example.test"
    provider = DemoRestProvider()
    monkeypatch.setattr(provider, "_post", lambda path, json: {"id": "bk-1", "status": "confirmed"})
    result = provider.create_booking(
        place_ref="venue-9",
        start=datetime(2026, 1, 1, 10, 0),
        end=datetime(2026, 1, 1, 11, 0),
        party_size=4,
        user_ref="user-uuid",
    )
    assert result.external_ref == "bk-1"
    assert result.confirmed is True

    cancelled = {}
    monkeypatch.setattr(
        provider, "_post", lambda path, json: cancelled.update({"path": path}) or {}
    )
    provider.cancel(external_ref="bk-1")
    assert cancelled["path"] == "/bookings/bk-1/cancel"


def test_demo_rest_unconfigured_raises(monkeypatch, settings):
    settings.BOOKING_DEMO_BASE_URL = ""
    provider = DemoRestProvider()
    with pytest.raises(BookingError):
        provider.create_booking(
            place_ref="v", start=datetime(2026, 1, 1), end=None, party_size=1, user_ref="u"
        )
