import pytest
from rest_framework.test import APIClient


@pytest.fixture
def client(adult):
    c = APIClient()
    c.force_authenticate(adult)
    return c


@pytest.mark.django_db
def test_options_endpoint(client, place):
    resp = client.get("/api/booking/options/", {"place": place.pk})
    assert resp.status_code == 200
    assert resp.json()["provider"] == "deeplink"


@pytest.mark.django_db
def test_providers_endpoint(client):
    resp = client.get("/api/booking/providers/")
    assert resp.status_code == 200
    slugs = {p["slug"] for p in resp.json()}
    assert {"deeplink", "demo_rest"} <= slugs


@pytest.mark.django_db
def test_create_and_list_and_cancel(client, place, now):
    resp = client.post(
        "/api/booking/bookings/",
        {"place": place.pk, "starts_at": now.isoformat()},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    booking_id = resp.json()["id"]
    assert resp.json()["status"] == "pending"

    listing = client.get("/api/booking/bookings/")
    assert listing.status_code == 200
    assert any(b["id"] == booking_id for b in listing.json()["results"])

    cancel = client.post(f"/api/booking/bookings/{booking_id}/cancel/")
    assert cancel.status_code == 200
    assert cancel.json()["status"] == "cancelled"


@pytest.mark.django_db
def test_requires_auth(place):
    resp = APIClient().get("/api/booking/options/", {"place": place.pk})
    assert resp.status_code in (401, 403)
