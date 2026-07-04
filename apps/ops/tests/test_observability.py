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


def test_run_due_jobs_stamps_a_run_id_into_the_logging_context(monkeypatch):
    # Outside an HTTP request the correlation id defaults to "-"; run_due_jobs must stamp a per-run
    # id so every job's logs are correlated. We capture get_request_id() DURING each job.
    from django.core.management import call_command

    import apps.ops.management.commands.run_due_jobs as mod
    from apps.ops.observability import get_request_id, set_request_id

    set_request_id("-")
    seen = []
    monkeypatch.setattr(mod, "call_command", lambda *a, **k: seen.append(get_request_id()))
    call_command("run_due_jobs")
    set_request_id("-")  # don't leak the run id into other tests' context
    assert seen and all(rid.startswith("job:run_due_jobs:") for rid in seen)
    assert len(set(seen)) == 1  # one id for the whole run


# --- CSP violation-report collector --------------------------------------------------------


_CSP_REPORT_URL = "/api/v1/ops/csp-report/"  # apps.ops.urls is mounted under /api/v1/


def test_csp_header_carries_report_directives_and_reporting_endpoints():
    resp = APIClient().get("/healthz")
    csp = resp.get("Content-Security-Policy-Report-Only", "")
    assert f"report-uri {_CSP_REPORT_URL}" in csp
    assert "report-to csp" in csp
    assert f'csp="{_CSP_REPORT_URL}"' in resp.get("Reporting-Endpoints", "")


def test_csp_report_endpoint_204s_anonymously():
    body = {
        "csp-report": {
            "effective-directive": "script-src",
            "blocked-uri": "https://evil.example/x.js",
            "document-uri": "https://meet.test/page",
        }
    }
    # No auth, no CSRF token — a browser-driven report must always be accepted.
    assert APIClient().post(_CSP_REPORT_URL, body, format="json").status_code == 204
    # A garbage / unparseable body is also tolerated (never a 500).
    garbage = APIClient().post(_CSP_REPORT_URL, b"not json", content_type="text/plain")
    assert garbage.status_code == 204


def test_csp_report_logs_only_operational_fields():
    import logging

    from django.core.cache import cache

    cache.delete("csp-report-log-budget")  # fresh log budget for a deterministic assert
    body = {
        "csp-report": {
            "effective-directive": "img-src",
            "blocked-uri": "https://bad.example/pic",
            "document-uri": "https://meet.test/a",
        }
    }
    # Attach directly to the logger (the "apps" logger has propagate=False, so caplog's root
    # handler wouldn't see it).
    records = []
    handler = logging.Handler()
    handler.emit = records.append
    logger = logging.getLogger("apps.ops.csp_report")
    logger.addHandler(handler)
    try:
        APIClient().post(_CSP_REPORT_URL, body, format="json")
    finally:
        logger.removeHandler(handler)
    msgs = [r.getMessage() for r in records]
    assert any("CSP violation" in m and "img-src" in m for m in msgs)


def test_csp_report_strips_control_chars_to_prevent_log_forging():
    import logging

    from django.core.cache import cache

    cache.delete("csp-report-log-budget")
    body = {
        "csp-report": {
            "effective-directive": "script-src",
            "blocked-uri": "https://x/\r\n2026-01-01 INFO forged-line",  # attacker-controlled
            "document-uri": "https://meet.test/",
        }
    }
    records = []
    handler = logging.Handler()
    handler.emit = records.append
    logger = logging.getLogger("apps.ops.csp_report")
    logger.addHandler(handler)
    try:
        APIClient().post(_CSP_REPORT_URL, body, format="json")
    finally:
        logger.removeHandler(handler)
    assert records
    msg = records[0].getMessage()
    assert "\n" not in msg and "\r" not in msg  # CRLF stripped -> no forged second log line
    assert "forged-line" in msg  # the (sanitised) content is still there, just on one line


def test_csp_report_digest_groups_violations_and_counts_malformed_payloads():
    from apps.ops.csp import digest_csp_reports

    summary = digest_csp_reports(
        [
            {
                "csp-report": {
                    "effective-directive": "style-src-attr",
                    "blocked-uri": "inline",
                    "document-uri": "https://meet.test/activities/1/?secret=drop",
                }
            },
            {
                "csp-report": {
                    "violated-directive": "style-src-attr 'self'",
                    "blocked-uri": "inline",
                    "document-uri": "https://meet.test/activities/1/#frag",
                }
            },
            b"not json",
            [{"body": {"effective-directive": "script-src", "blockedURL": "data:text/js,evil"}}],
        ]
    )
    assert summary["total"] == 3
    assert summary["malformed"] == 1
    assert summary["groups"][0] == {
        "count": 2,
        "directive": "style-src-attr",
        "blocked": "inline",
        "document": "https://meet.test/activities/1/",
    }
    assert summary["groups"][1]["directive"] == "script-src"


def test_json_formatter_never_leaks_a_user_object_on_the_record():
    # Guard the allowlist: a future `logger.info("x", extra={"user": u})` attaches the user to the
    # record, but JsonFormatter must emit ONLY its fixed operational fields — never the user's PII.
    import json
    import logging

    from apps.ops.observability import JsonFormatter

    class _FakeUser:
        username = "alice.secret"
        email = "alice@example.com"
        display_name = "Alice Secret"

        def __str__(self):
            return self.username  # even a PII-leaking __str__ must not reach the JSON

    rec = logging.LogRecord("apps.demo", logging.INFO, __file__, 1, "did a thing", (), None)
    rec.user = _FakeUser()
    rec.request_id = "rid"
    out = JsonFormatter().format(rec)
    assert "alice.secret" not in out
    assert "alice@example.com" not in out
    assert "Alice Secret" not in out
    assert set(json.loads(out)) <= {"ts", "level", "logger", "msg", "request_id", "exc"}
