import pytest
from django.test import override_settings
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.donations.models import Donation
from apps.donations.services import DonationError, complete_donation, start_donation

pytestmark = pytest.mark.django_db

DEV_PROVIDER = "apps.donations.providers.DevPaymentProvider"


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


@override_settings(DONATIONS_PROVIDER=DEV_PROVIDER)
def test_start_donation_creates_pending_with_no_card_data():
    user = _user("d1")
    donation, url = start_donation(user, 500, "EUR")
    assert donation.status == Donation.Status.PENDING
    assert donation.amount_cents == 500
    assert donation.donor == user
    assert donation.external_ref and donation.external_ref in url
    # No card/payment fields exist on the model — only an opaque reference.
    assert not any("card" in f.name for f in Donation._meta.get_fields())


@override_settings(DONATIONS_PROVIDER=DEV_PROVIDER)
def test_anonymous_donation_allowed():
    class Anon:
        is_authenticated = False

    donation, _ = start_donation(Anon(), 1000)
    assert donation.donor is None


def test_below_minimum_rejected():
    with pytest.raises(DonationError):
        start_donation(None, 50)


@override_settings(DONATIONS_PROVIDER=DEV_PROVIDER)
def test_complete_donation_via_reference():
    donation, _ = start_donation(_user("d2"), 700)
    completed = complete_donation(donation.external_ref)
    assert completed.status == Donation.Status.COMPLETED
    assert completed.completed_at is not None
    # Idempotent: a second callback finds nothing pending.
    assert complete_donation(donation.external_ref) is None


@override_settings(DONATIONS_PROVIDER=DEV_PROVIDER)
def test_donation_api_anonymous():
    resp = APIClient().post(
        "/api/donations/", {"amount_cents": 1500, "currency": "EUR"}, format="json"
    )
    assert resp.status_code == 201, resp.content
    assert "checkout_url" in resp.json()
    assert Donation.objects.count() == 1
