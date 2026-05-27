"""Booking provider adapter interface.

There is no universal booking standard — coverage is per-provider (see
docs/DATA_AND_INTEGRATIONS.md). So we phase it: a **deep-link** baseline that works
for every place (surface "how to book"), then per-provider REST integrations behind
this common interface. The rest of the app depends only on ``BookingProvider``.
"""

from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from datetime import datetime


class BookingError(Exception):
    """A booking operation could not be completed."""


class BookingNotSupported(BookingError):
    """The provider does not support this operation (e.g. deep-link only)."""


@dataclass
class Slot:
    start: datetime
    end: datetime
    available: bool = True


@dataclass
class BookingResult:
    external_ref: str
    confirmed: bool = True
    raw: dict = field(default_factory=dict)


class BookingProvider(ABC):
    """One implementation per booking platform. ``supports_realtime`` is False for
    deep-link-only providers (no API), True when availability/booking calls work."""

    name: str
    supports_realtime: bool = False

    def booking_url(self, *, place_ref: str = "", info=None) -> str | None:
        """Return a "how to book" deep link for this place, or None."""
        return getattr(info, "deep_link", "") or None

    def availability(self, *, place_ref: str, start: datetime, end: datetime) -> list[Slot]:
        raise BookingNotSupported(f"{self.name} does not expose availability")

    @abstractmethod
    def create_booking(
        self,
        *,
        place_ref: str,
        start: datetime,
        end: datetime | None,
        party_size: int,
        user_ref: str,
    ) -> BookingResult:
        raise NotImplementedError

    def cancel(self, *, external_ref: str) -> None:
        raise BookingNotSupported(f"{self.name} does not support cancellation")
