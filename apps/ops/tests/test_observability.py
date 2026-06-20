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


# --- request correlation + structured logging ---------------------------------------------


def test_request_id_minted_and_echoed_on_response():
    rid = APIClient().get("/healthz").get("X-Request-ID")
    assert rid and len(rid) >= 8  # a minted uuid hex


def test_request_id_trusts_a_bounded_inbound_value():
    resp = APIClient().get("/healthz", HTTP_X_REQUEST_ID="trace-abc-123")
    assert resp.get("X-Request-ID") == "trace-abc-123"


def test_json_formatter_emits_request_id_and_fields():
    import json
    import logging

    from apps.ops.observability import JsonFormatter

    rec = logging.LogRecord("apps.demo", logging.INFO, __file__, 1, "hello %s", ("world",), None)
    rec.request_id = "rid-xyz"
    out = json.loads(JsonFormatter().format(rec))
    assert out["request_id"] == "rid-xyz"
    assert out["level"] == "INFO" and out["logger"] == "apps.demo" and out["msg"] == "hello world"


# --- run_due_jobs: Sentry capture on failure + heartbeat on success -----------------------


@override_settings(OPS_HEARTBEAT_URL="https://ping.example/abc")
def test_run_due_jobs_pings_heartbeat_only_on_clean_run(monkeypatch):
    import requests
    from django.core.management import call_command

    import apps.ops.management.commands.run_due_jobs as mod

    monkeypatch.setattr(mod, "call_command", lambda *a, **k: None)  # every job a no-op success
    pings = []
    monkeypatch.setattr(requests, "get", lambda url, **k: pings.append(url))
    call_command("run_due_jobs")
    assert pings == ["https://ping.example/abc"]


def test_run_due_jobs_captures_failures_to_sentry(monkeypatch):
    import sentry_sdk
    from django.core.management import call_command
    from django.core.management.base import CommandError

    import apps.ops.management.commands.run_due_jobs as mod

    def boom(name, **k):
        raise RuntimeError(f"{name} boom")

    monkeypatch.setattr(mod, "call_command", boom)  # every job fails
    captured = []
    monkeypatch.setattr(sentry_sdk, "capture_exception", lambda exc, *a, **k: captured.append(exc))
    with pytest.raises(CommandError):  # a failing run still raises so the scheduler sees it
        call_command("run_due_jobs")
    assert captured  # the failures were reported to Sentry


def test_request_id_rejects_crlf_injection():
    # A forged inbound id with CR/LF must NOT 500 (observability never breaks a request) and must
    # NOT be reflected verbatim into the response header or logs — it is replaced with a minted id.
    resp = APIClient().get("/healthz", HTTP_X_REQUEST_ID="a\r\nSet-Cookie: evil=1")
    assert resp.status_code == 200
    rid = resp.get("X-Request-ID")
    assert "\r" not in rid and "\n" not in rid
    assert rid != "a\r\nSet-Cookie: evil=1"  # replaced, not reflected
    assert len(rid) == 32  # a freshly minted uuid hex
