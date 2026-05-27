"""Deep-link baseline provider: works for every place, zero integration.

It cannot make a reservation programmatically — it only surfaces the provider's
"how to book" link/instructions. Bookings created through it stay PENDING (the user
completes the reservation on the provider's site).
"""

from __future__ import annotations

from .base import BookingNotSupported, BookingProvider, BookingResult


class DeepLinkProvider(BookingProvider):
    name = "deeplink"
    supports_realtime = False

    def create_booking(self, **kwargs) -> BookingResult:
        raise BookingNotSupported(
            "deep-link provider cannot create reservations; follow the booking_url"
        )
