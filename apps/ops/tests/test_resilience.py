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


# --- half-open recovery + lock safety (unit-level state machine) ---------------------------


def test_breaker_half_open_admits_one_probe_then_closes_on_success():
    b = CircuitBreaker(threshold=2, cooldown=0.0)  # cooldown 0 -> probe available immediately
    b.record_failure()
    b.record_failure()
    assert b.state == CircuitBreaker.OPEN
    # First allow() after the cooldown enters HALF_OPEN and reserves the single probe slot.
    assert b.allow() is True
    assert b.state == CircuitBreaker.HALF_OPEN
    assert b.allow() is False  # only half_open_max (=1) probe is admitted; the rest fail fast
    b.record_success()  # success_threshold (=1) reached -> CLOSED
    assert b.state == CircuitBreaker.CLOSED
    assert b.allow() is True


def test_breaker_half_open_failure_reopens_with_fresh_cooldown():
    b = CircuitBreaker(threshold=1, cooldown=0.0)
    b.record_failure()  # threshold 1 -> OPEN
    assert b.allow() is True  # the half-open probe
    b.record_failure()  # the probe failed -> straight back to OPEN
    assert b.state == CircuitBreaker.OPEN


def test_breaker_success_threshold_requires_consecutive_probes():
    b = CircuitBreaker(threshold=1, cooldown=0.0, success_threshold=2)
    b.record_failure()
    assert b.allow() is True  # probe 1
    b.record_success()  # 1/2 — still half-open
    assert b.state == CircuitBreaker.HALF_OPEN
    assert b.allow() is True  # probe 2 (slot freed by the prior record_success)
    b.record_success()  # 2/2 -> CLOSED
    assert b.state == CircuitBreaker.CLOSED


def test_breaker_open_stays_open_during_cooldown_then_reset():
    b = CircuitBreaker(threshold=1, cooldown=999)
    b.record_failure()
    assert b.allow() is False  # OPEN, long cooldown -> fail fast
    b.reset()
    assert b.state == CircuitBreaker.CLOSED
    assert b.allow() is True


def test_breaker_failure_count_is_lock_safe_under_threads():
    import threading

    b = CircuitBreaker(threshold=10_000, cooldown=0.0)  # high threshold so it never opens here

    def hammer():
        for _ in range(1000):
            b.record_failure()

    threads = [threading.Thread(target=hammer) for _ in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    # A concurrent-access smoke test: many record_failure() calls interleave without crashing and
    # land the exact count. NOTE this alone is not a definitive lock guard — under CPython's GIL a
    # plain ``+=`` rarely loses updates, so it could pass even without the lock. The AUTHORITATIVE
    # lock guard is test_breaker_half_open_admits_only_one_probe_under_contention below, whose wide
    # check-then-reserve window genuinely goes red if the lock is removed.
    assert b.failures == 8000


def test_breaker_half_open_admits_only_one_probe_under_contention():
    import threading

    b = CircuitBreaker(threshold=1, cooldown=0.0)
    b.record_failure()  # OPEN with a 0s cooldown -> the very next allow() can probe
    results = []
    guard = threading.Lock()

    def probe():
        ok = b.allow()
        with guard:
            results.append(ok)

    threads = [threading.Thread(target=probe) for _ in range(24)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()
    # The lock makes the check-then-reserve atomic, so EXACTLY one probe is admitted (without it,
    # several threads could pass the budget check before any increment and all be let through).
    assert results.count(True) == 1
    assert results.count(False) == 23
