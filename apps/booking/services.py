"""Booking domain logic. Views call these; gating lives here.

Phasing (docs/DATA_AND_INTEGRATIONS.md): deep-links work everywhere and produce a
PENDING booking the user completes on the provider; REST providers confirm in-app.
"""

from __future__ import annotations

from datetime import datetime

from django.db import transaction

from apps.accounts.services import can_participate
from apps.places.models import Place
from apps.social.models import Activity
from apps.social.services import current_members

from .models import Booking, PlaceBookingInfo
from .providers.base import BookingError
from .registry import DEFAULT_PROVIDER, get_booking_provider


class BookingDenied(Exception):
    """The user is not allowed to make this booking."""


def booking_options(place: Place) -> dict:
    """How a user can book this place: provider, whether in-app booking is
    supported, a deep link and instructions."""
    info = getattr(place, "booking_info", None)
    provider_slug = info.provider if info else DEFAULT_PROVIDER
    provider = get_booking_provider(provider_slug)
    deep_link = (info.deep_link if info else "") or ""
    return {
        "provider": provider_slug,
        "bookable_in_app": provider.supports_realtime,
        "deep_link": provider.booking_url(info=info) or deep_link,
        "instructions": info.instructions if info else "",
    }


def _resolve_provider_slug(
    place: Place, explicit: str | None
) -> tuple[str, PlaceBookingInfo | None]:
    info = getattr(place, "booking_info", None)
    slug = explicit or (info.provider if info else DEFAULT_PROVIDER)
    return slug, info


@transaction.atomic
def create_booking(
    user,
    *,
    place: Place,
    starts_at: datetime,
    ends_at: datetime | None = None,
    party_size: int = 1,
    activity: Activity | None = None,
    provider: str | None = None,
) -> Booking:
    """Create a booking. REST providers confirm immediately; deep-link providers
    yield a PENDING booking carrying the link to finish on the provider's site."""
    if not can_participate(user):
        raise BookingDenied("user is not eligible to participate")
    if activity is not None and not current_members(activity).filter(user=user).exists():
        raise BookingDenied("only members of an activity can book for it")

    slug, info = _resolve_provider_slug(place, provider)
    adapter = get_booking_provider(slug)
    place_ref = (info.provider_place_ref if info else "") or str(place.pk)

    booking = Booking(
        user=user,
        place=place,
        activity=activity,
        provider=slug,
        starts_at=starts_at,
        ends_at=ends_at,
        party_size=party_size,
    )

    if adapter.supports_realtime:
        result = adapter.create_booking(
            place_ref=place_ref,
            start=starts_at,
            end=ends_at,
            party_size=party_size,
            user_ref=str(user.public_id),
        )
        booking.external_ref = result.external_ref
        booking.status = Booking.Status.CONFIRMED if result.confirmed else Booking.Status.PENDING
    else:
        booking.deep_link = adapter.booking_url(info=info) or ""
        booking.status = Booking.Status.PENDING

    booking.save()
    return booking


@transaction.atomic
def cancel_booking(user, booking: Booking) -> Booking:
    if booking.user_id != user.id:
        raise BookingDenied("cannot cancel another user's booking")
    if booking.status == Booking.Status.CANCELLED:
        return booking
    adapter = get_booking_provider(booking.provider)
    if adapter.supports_realtime and booking.external_ref:
        try:
            adapter.cancel(external_ref=booking.external_ref)
        except BookingError:
            # Cancellation failed provider-side; surface as failed, don't crash.
            booking.status = Booking.Status.FAILED
            booking.save(update_fields=["status", "updated_at"])
            raise
    booking.status = Booking.Status.CANCELLED
    booking.save(update_fields=["status", "updated_at"])
    return booking
