import pytest
from django.test import override_settings
from rest_framework.test import APIClient

from apps.donations.models import Donation
from apps.donations.services import completed_total_cents, start_donation

pytestmark = pytest.mark.django_db

DEV = "apps.donations.providers.DevPaymentProvider"


@override_settings(DONATIONS_PROVIDER=DEV)
def test_recurring_flag_persists():
    donation, _ = start_donation(None, 500, recurring=True)
    assert donation.recurring is True


@override_settings(DONATIONS_PROVIDER=DEV)
def test_completed_total_aggregates_only_completed():
    d1, _ = start_donation(None, 500)
    d2, _ = start_donation(None, 1500)
    assert completed_total_cents() == 0
    d1.mark_completed()
    assert completed_total_cents() == 500
    d2.mark_completed()
    assert completed_total_cents() == 2000


@override_settings(DONATIONS_PROVIDER=DEV)
def test_total_endpoint_public():
    d, _ = start_donation(None, 700)
    d.mark_completed()
    resp = APIClient().get("/api/donations/total/")
    assert resp.status_code == 200
    assert resp.json() == {"currency": "EUR", "total_cents": 700}


@override_settings(DONATIONS_PROVIDER=DEV, DONATIONS_WEBHOOK_SECRET="s3cret")
def test_webhook_completes_with_secret():
    donation, _ = start_donation(None, 900)
    client = APIClient()
    # Wrong secret rejected.
    bad = client.post(
        "/api/donations/webhook/", {"external_ref": donation.external_ref}, format="json"
    )
    assert bad.status_code == 403

    ok = client.post(
        "/api/donations/webhook/",
        {"external_ref": donation.external_ref},
        format="json",
        HTTP_X_WEBHOOK_SECRET="s3cret",
    )
    assert ok.status_code == 200
    donation.refresh_from_db()
    assert donation.status == Donation.Status.COMPLETED


@override_settings(DONATIONS_PROVIDER=DEV, DONATIONS_WEBHOOK_SECRET="")
def test_webhook_unknown_ref_ignored():
    resp = APIClient().post("/api/donations/webhook/", {"external_ref": "nope"}, format="json")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ignored"
