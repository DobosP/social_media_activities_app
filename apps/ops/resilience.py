"""Resilient outbound HTTP to TRUSTED provider APIs (Stripe, booking).

Bounded retries with backoff + a per-provider circuit breaker, raising a clean
:class:`ProviderUnavailable` instead of a raw ``requests`` exception (which would surface as a 500
to the donor/booker). Distinct from :mod:`apps.safety.net` (SSRF-hardening for operator/feed-
configured URLs): these endpoints are FIXED + trusted, so the concern is RELIABILITY, not SSRF.

RETRY SAFETY — the load-bearing rule. Retrying a NON-idempotent POST can double-fire (a duplicate
charge / double booking). So:

* Pass ``retry_on_status`` (5xx) ONLY for calls that are idempotent OR carry an idempotency key
  (Stripe's ``Idempotency-Key`` header makes a retried POST safe — Stripe dedupes it).
* For a non-idempotent POST, pass ``retry_on_status=()`` and ``retry_timeouts=False`` so ONLY a
  connection error (where the request never reached the server) is retried — never a 5xx or a read
  timeout (which may have been processed server-side).
"""

from __future__ import annotations

import logging
import threading
import time

logger = logging.getLogger(__name__)
DEFAULT_RETRY_STATUS = (500, 502, 503, 504)


class ProviderUnavailable(Exception):
    """A trusted external provider failed transiently after retries, or its breaker is open."""


class CircuitBreaker:
    """A tiny per-key breaker: after ``threshold`` consecutive failures it OPENS for ``cooldown``
    seconds, failing fast so a down provider doesn't tie up a worker for ``timeout * attempts`` on
    every call. State is IN-PROCESS (a module dict) — on a multi-worker deploy each process has its
    own breaker; a cross-process breaker would need shared state (Redis). Adequate per-process
    fail-fast; the cost of being wrong is at most one wasted call per process per cooldown."""

    _registry: dict[str, CircuitBreaker] = {}
    _lock = threading.Lock()

    def __init__(self, threshold: int, cooldown: float):
        self.threshold = threshold
        self.cooldown = cooldown
        self.failures = 0
        self.open_until = 0.0

    @classmethod
    def get(cls, key: str, *, threshold: int = 5, cooldown: float = 30.0) -> CircuitBreaker:
        with cls._lock:
            return cls._registry.setdefault(key, cls(threshold, cooldown))

    def allow(self) -> bool:
        return time.monotonic() >= self.open_until

    def record_success(self) -> None:
        self.failures = 0
        self.open_until = 0.0

    def record_failure(self) -> None:
        self.failures += 1
        if self.failures >= self.threshold:
            self.open_until = time.monotonic() + self.cooldown


def request_with_retries(
    method: str,
    url: str,
    *,
    max_attempts: int = 3,
    timeout: float = 15,
    backoff: float = 0.5,
    retry_on_status=DEFAULT_RETRY_STATUS,
    retry_timeouts: bool = True,
    breaker_key: str | None = None,
    **kwargs,
):
    """Make ``method url`` with bounded retries; return the ``requests.Response``.

    Raises :class:`ProviderUnavailable` for a transient failure (a retryable 5xx, a connection
    error, or — when ``retry_timeouts`` — a timeout, after ``max_attempts``; or an open breaker).
    Re-raises ``requests.HTTPError`` for a 4xx (a permanent/client error — not retried, never trips
    the breaker)."""
    import requests

    breaker = CircuitBreaker.get(breaker_key) if breaker_key else None
    if breaker is not None and not breaker.allow():
        raise ProviderUnavailable(f"{breaker_key}: circuit open after repeated failures")

    last_exc: Exception | None = None
    for attempt in range(1, max_attempts + 1):
        try:
            resp = requests.request(method, url, timeout=timeout, **kwargs)
        except requests.RequestException as exc:  # connection / timeout — request did not complete
            last_exc = exc
            # A connection error means the request never reached the server (always safe to retry);
            # a timeout is only retried when the caller says so (idempotent/keyed).
            safe = isinstance(exc, requests.ConnectionError) or (
                retry_timeouts and isinstance(exc, requests.Timeout)
            )
            if safe and attempt < max_attempts:
                time.sleep(backoff * attempt)
                continue
            break

        if resp.status_code in retry_on_status and attempt < max_attempts:
            logger.warning(
                "provider %s HTTP %s (attempt %s/%s); retrying",
                url,
                resp.status_code,
                attempt,
                max_attempts,
            )
            time.sleep(backoff * attempt)
            continue
        if 400 <= resp.status_code < 500:
            if breaker is not None:
                breaker.record_success()  # the provider IS up — this is a client/permanent error
            resp.raise_for_status()  # raises HTTPError(4xx) out of the helper
        if resp.status_code >= 500:
            last_exc = requests.HTTPError(f"HTTP {resp.status_code}", response=resp)
            break
        if breaker is not None:
            breaker.record_success()
        return resp

    if breaker is not None:
        breaker.record_failure()
    raise ProviderUnavailable(
        f"{method} {url} failed after {max_attempts} attempt(s): {last_exc}"
    ) from last_exc
