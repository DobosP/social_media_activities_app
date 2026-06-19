"""Resilient provider HTTP (P1): bounded retries with the correct POST-retry-safety rules, a
ProviderUnavailable on exhaustion, and a per-provider circuit breaker that fails fast when open."""

from unittest.mock import MagicMock, patch

import pytest
import requests

from apps.ops.resilience import CircuitBreaker, ProviderUnavailable, request_with_retries


def _resp(status):
    r = MagicMock()
    r.status_code = status
    r.json.return_value = {"ok": True}
    if 400 <= status < 500:
        r.raise_for_status.side_effect = requests.HTTPError(f"HTTP {status}", response=r)
    else:
        r.raise_for_status.return_value = None
    return r


@patch("requests.request")
def test_retries_5xx_then_succeeds(mock_req):
    mock_req.side_effect = [_resp(503), _resp(200)]
    resp = request_with_retries("GET", "https://x", backoff=0)
    assert resp.status_code == 200
    assert mock_req.call_count == 2


@patch("requests.request")
def test_exhausts_to_provider_unavailable(mock_req):
    mock_req.return_value = _resp(503)
    with pytest.raises(ProviderUnavailable):
        request_with_retries("GET", "https://x", max_attempts=2, backoff=0)
    assert mock_req.call_count == 2


@patch("requests.request")
def test_non_idempotent_post_does_not_retry_5xx(mock_req):
    # retry_on_status=() -> a 5xx for a non-idempotent POST is NOT retried (could double-fire).
    mock_req.return_value = _resp(503)
    with pytest.raises(ProviderUnavailable):
        request_with_retries("POST", "https://x", retry_on_status=(), backoff=0)
    assert mock_req.call_count == 1


@patch("requests.request")
def test_connection_error_is_retried(mock_req):
    # A connection error means the request never reached the server -> safe to retry even for POST.
    mock_req.side_effect = [requests.ConnectionError("x"), _resp(200)]
    resp = request_with_retries(
        "POST", "https://x", retry_on_status=(), retry_timeouts=False, backoff=0
    )
    assert resp.status_code == 200
    assert mock_req.call_count == 2


@patch("requests.request")
def test_read_timeout_not_retried_when_disabled(mock_req):
    # A read timeout MAY have been processed server-side -> never retried for a non-idempotent POST.
    mock_req.side_effect = requests.ReadTimeout("slow")
    with pytest.raises(ProviderUnavailable):
        request_with_retries(
            "POST", "https://x", retry_on_status=(), retry_timeouts=False, backoff=0
        )
    assert mock_req.call_count == 1


@patch("requests.request")
def test_4xx_reraised_as_httperror_not_provider_unavailable(mock_req):
    # A client/permanent error surfaces as HTTPError (caller decides) — not a transient-unavailable.
    mock_req.return_value = _resp(400)
    with pytest.raises(requests.HTTPError):
        request_with_retries("GET", "https://x", backoff=0)


@patch("requests.request")
def test_breaker_opens_after_threshold_and_fails_fast(mock_req):
    CircuitBreaker._registry.clear()
    mock_req.return_value = _resp(503)
    key = "test-breaker"
    for _ in range(5):  # default threshold = 5; each call = 1 attempt = 1 failure
        with pytest.raises(ProviderUnavailable):
            request_with_retries("GET", "https://x", max_attempts=1, backoff=0, breaker_key=key)
    calls = mock_req.call_count
    # Breaker is now open -> the next call fails fast WITHOUT making a request.
    with pytest.raises(ProviderUnavailable):
        request_with_retries("GET", "https://x", max_attempts=1, backoff=0, breaker_key=key)
    assert mock_req.call_count == calls
    CircuitBreaker._registry.clear()
