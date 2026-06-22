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
    """A tiny per-key breaker with a CLOSED -> OPEN -> HALF_OPEN state machine:

    * CLOSED — calls pass; ``threshold`` consecutive failures OPEN it.
    * OPEN — calls fail fast for ``cooldown`` seconds (a down provider never ties up a worker for
      ``timeout * attempts`` on every call).
    * HALF_OPEN (once the cooldown elapses) — at most ``half_open_max`` probe call(s) are let
      through while everything else keeps failing fast; ``success_threshold`` consecutive probe
      successes CLOSE it, and ANY probe failure re-OPENS it with a fresh cooldown. So a flapping
      provider isn't hit by a thundering herd the instant the cooldown ends.

    Every transition holds ``self._lock``, so concurrent workers in one process can't lose a failure
    count nor admit more than ``half_open_max`` probes (the previous version mutated unguarded —
    benign under the GIL, but a real correctness smell once a probe budget exists). State is
    IN-PROCESS (a module dict): a cross-process breaker would need shared state (Redis); per-process
    fail-fast is adequate (worst case, a few wasted probes per process per cooldown).

    Contract: a caller MUST pair every ``allow() is True`` with exactly one ``record_success`` /
    ``record_failure`` — because in HALF_OPEN ``allow`` reserves a probe slot.
    ``request_with_retries`` upholds it (one ``allow`` at entry, one ``record_*`` per call)."""

    CLOSED, OPEN, HALF_OPEN = "closed", "open", "half_open"

    _registry: dict[str, CircuitBreaker] = {}
    _registry_lock = threading.Lock()

    def __init__(
        self,
        threshold: int,
        cooldown: float,
        *,
        success_threshold: int = 1,
        half_open_max: int = 1,
    ):
        self.threshold = threshold
        self.cooldown = cooldown
        self.success_threshold = max(1, success_threshold)
        self.half_open_max = max(1, half_open_max)
        self._lock = threading.Lock()
        self.state = self.CLOSED
        self.failures = 0
        self.successes = 0
        self.open_until = 0.0
        self._probes = 0  # in-flight HALF_OPEN probe calls

    @classmethod
    def get(
        cls,
        key: str,
        *,
        threshold: int = 5,
        cooldown: float = 30.0,
        success_threshold: int = 1,
        half_open_max: int = 1,
    ) -> CircuitBreaker:
        with cls._registry_lock:
            return cls._registry.setdefault(
                key,
                cls(
                    threshold,
                    cooldown,
                    success_threshold=success_threshold,
                    half_open_max=half_open_max,
                ),
            )

    def allow(self) -> bool:
        """Reserve permission to make ONE call (see the contract in the class docstring)."""
        with self._lock:
            if self.state == self.OPEN:
                if time.monotonic() < self.open_until:
                    return False
                # Cooldown elapsed: enter HALF_OPEN and let THIS call be the first probe.
                self.state = self.HALF_OPEN
                self.successes = 0
                self._probes = 0
            if self.state == self.HALF_OPEN:
                if self._probes >= self.half_open_max:
                    return False
                self._probes += 1
                return True
            return True  # CLOSED

    def record_success(self) -> None:
        with self._lock:
            if self.state == self.HALF_OPEN:
                self._probes = max(0, self._probes - 1)
                self.successes += 1
                if self.successes >= self.success_threshold:
                    self._reset_locked()
            else:
                self.failures = 0  # a success clears the consecutive-failure streak

    def record_failure(self) -> None:
        with self._lock:
            if self.state == self.HALF_OPEN:
                self._probes = max(0, self._probes - 1)
                self._open_locked(time.monotonic())  # a probe failed -> straight back to OPEN
            else:
                self.failures += 1
                if self.failures >= self.threshold:
                    self._open_locked(time.monotonic())

    def reset(self) -> None:
        """Force the breaker CLOSED (admin / test tooling)."""
        with self._lock:
            self._reset_locked()

    def _open_locked(self, now: float) -> None:
        self.state = self.OPEN
        self.open_until = now + self.cooldown
        self.successes = 0
        self._probes = 0

    def _reset_locked(self) -> None:
        self.state = self.CLOSED
        self.failures = 0
        self.successes = 0
        self._probes = 0
        self.open_until = 0.0


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
