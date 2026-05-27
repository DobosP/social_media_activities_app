import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance

pytestmark = pytest.mark.django_db


def test_healthz_is_public_and_reports_db():
    resp = APIClient().get("/healthz")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ok"
    assert body["database"] is True


def test_stats_requires_staff():
    user = User.objects.create_user(username="plain", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    client = APIClient()
    client.force_authenticate(user)
    assert client.get("/api/ops/stats/").status_code == 403


def test_stats_returns_aggregate_only_for_staff():
    staff = User.objects.create_superuser(username="admin", password="pw")
    client = APIClient()
    client.force_authenticate(staff)
    resp = client.get("/api/ops/stats/")
    assert resp.status_code == 200
    body = resp.json()
    # Aggregate counts only — no per-user data, no PII.
    assert set(body) == {
        "users",
        "activities",
        "posts",
        "bookings",
        "donations_completed",
        "donations_total_cents",
    }
    assert body["users"] >= 1
