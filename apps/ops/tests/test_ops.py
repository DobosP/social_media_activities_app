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


def test_readyz_reports_ready_with_db_up():
    resp = APIClient().get("/readyz")
    assert resp.status_code == 200
    body = resp.json()
    assert body["status"] == "ready"
    assert body["database"] is True
    # Cache/storage keys appear ONLY when those shared deps are configured (Redis / S3).
    assert "cache" not in body  # no REDIS_URL in tests
    assert "storage" not in body  # LocalStorageBackend in tests


def test_readyz_degraded_503_when_db_down(monkeypatch):
    from apps.ops import views as ops_views

    monkeypatch.setattr(ops_views.ReadyView, "_check_db", lambda self: False)
    resp = APIClient().get("/readyz")
    assert resp.status_code == 503
    assert resp.json()["status"] == "degraded"


def test_bounded_pagination_caps_limit():
    from rest_framework.request import Request
    from rest_framework.test import APIRequestFactory

    from apps.ops.pagination import BoundedLimitOffsetPagination

    paginator = BoundedLimitOffsetPagination()
    req = Request(APIRequestFactory().get("/?limit=5000"))
    assert paginator.get_limit(req) == 200  # capped, not 5000


def test_permissions_policy_header_present():
    resp = APIClient().get("/healthz")
    assert "camera=()" in resp["Permissions-Policy"]
    assert "geolocation=(self)" in resp["Permissions-Policy"]


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
