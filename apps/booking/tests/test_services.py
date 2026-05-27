import pytest

from apps.accounts.models import AgeBand, User
from apps.booking.models import Booking, PlaceBookingInfo
from apps.booking.providers.demo_rest import DemoRestProvider
from apps.booking.services import BookingDenied, booking_options, cancel_booking, create_booking
from apps.social.services import create_activity


@pytest.mark.django_db
def test_booking_options_default_deeplink(place):
    opts = booking_options(place)
    assert opts["provider"] == "deeplink"
    assert opts["bookable_in_app"] is False


@pytest.mark.django_db
def test_booking_options_with_info(place):
    PlaceBookingInfo.objects.create(
        place=place,
        provider="deeplink",
        deep_link="https://book.example/hall",
        instructions="Call ahead",
    )
    opts = booking_options(place)
    assert opts["deep_link"] == "https://book.example/hall"
    assert opts["instructions"] == "Call ahead"


@pytest.mark.django_db
def test_create_deeplink_booking_is_pending(adult, place, now):
    PlaceBookingInfo.objects.create(
        place=place, provider="deeplink", deep_link="https://book.example/x"
    )
    booking = create_booking(adult, place=place, starts_at=now)
    assert booking.status == Booking.Status.PENDING
    assert booking.deep_link == "https://book.example/x"


@pytest.mark.django_db
def test_create_realtime_booking_confirms(monkeypatch, adult, place, now, settings):
    settings.BOOKING_DEMO_BASE_URL = "https://api.example.test"
    PlaceBookingInfo.objects.create(place=place, provider="demo_rest", provider_place_ref="venue-9")
    monkeypatch.setattr(
        DemoRestProvider, "_post", lambda self, path, json: {"id": "bk-9", "status": "confirmed"}
    )
    booking = create_booking(adult, place=place, starts_at=now)
    assert booking.status == Booking.Status.CONFIRMED
    assert booking.external_ref == "bk-9"
    assert booking.provider == "demo_rest"


@pytest.mark.django_db
def test_non_participant_denied(place, now):
    stranger = User.objects.create_user(username="stranger", password="pw")  # not verified
    with pytest.raises(BookingDenied):
        create_booking(stranger, place=place, starts_at=now)


@pytest.mark.django_db
def test_booking_for_activity_requires_membership(adult, place, activity_type, now):
    other = User.objects.create_user(username="outsider", password="pw")
    from apps.accounts.identity.base import AssuranceResult
    from apps.accounts.services import apply_assurance

    apply_assurance(other, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    other.refresh_from_db()

    activity = create_activity(
        adult, place=place, activity_type=activity_type, title="Game", starts_at=now
    )
    # Owner is a member -> allowed.
    booking = create_booking(adult, place=place, starts_at=now, activity=activity)
    assert booking.activity_id == activity.id
    # Outsider is not a member -> denied.
    with pytest.raises(BookingDenied):
        create_booking(other, place=place, starts_at=now, activity=activity)


@pytest.mark.django_db
def test_cancel_booking(adult, place, now):
    booking = create_booking(adult, place=place, starts_at=now)
    cancel_booking(adult, booking)
    booking.refresh_from_db()
    assert booking.status == Booking.Status.CANCELLED


@pytest.mark.django_db
def test_cannot_cancel_others_booking(adult, place, now):
    other = User.objects.create_user(username="o2", password="pw")
    booking = create_booking(adult, place=place, starts_at=now)
    with pytest.raises(BookingDenied):
        cancel_booking(other, booking)
