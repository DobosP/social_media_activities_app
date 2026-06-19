"""P1 observability/hardening: a report-only Content-Security-Policy on responses, and a Prometheus
/metrics endpoint that is CLOSED BY DEFAULT (bearer-token gated, never world-readable)."""

import pytest
from django.test import override_settings
from rest_framework.test import APIClient

pytestmark = pytest.mark.django_db


def test_csp_report_only_header_present_and_allows_leaflet():
    resp = APIClient().get("/healthz")
    csp = resp.get("Content-Security-Policy-Report-Only", "")
    assert "default-src 'self'" in csp
    assert "https://unpkg.com" in csp  # Leaflet CDN allowed
    assert "https://*.tile.openstreetmap.org" in csp  # OSM map tiles allowed
    # Report-only must NOT also be enforcing (no enforcing header set).
    assert resp.get("Content-Security-Policy", "") == ""


def test_metrics_forbidden_by_default():
    # No METRICS_TOKEN configured (test default) => the endpoint is closed.
    assert APIClient().get("/metrics").status_code == 403


@override_settings(METRICS_TOKEN="scrape-secret-token")
def test_metrics_ok_with_correct_bearer_token():
    resp = APIClient().get("/metrics", HTTP_AUTHORIZATION="Bearer scrape-secret-token")
    assert resp.status_code == 200
    assert b"# HELP" in resp.content  # Prometheus exposition format


@override_settings(METRICS_TOKEN="scrape-secret-token")
def test_metrics_forbidden_with_wrong_token():
    resp = APIClient().get("/metrics", HTTP_AUTHORIZATION="Bearer nope")
    assert resp.status_code == 403
