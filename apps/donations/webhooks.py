"""Donation webhook authentication.

The webhook marks a pending donation completed, so it must be authenticated — an
unauthenticated endpoint lets anyone forge completions. Two modes are supported:

* **Shared secret** (default): a constant-time comparison of the ``X-Webhook-Secret``
  header against ``DONATIONS_WEBHOOK_SECRET``. **Fail-closed**: if no secret is
  configured the webhook is rejected outright (never open-by-default).
* **Stripe signature**: when the Stripe provider is in use and ``STRIPE_WEBHOOK_SECRET``
  is set, the ``Stripe-Signature`` header is verified (HMAC-SHA256 over
  ``"{timestamp}.{raw_body}"``) with a replay-window tolerance.
"""

import hashlib
import hmac
import time


def constant_time_secret_ok(provided: str, expected: str) -> bool:
    """True only if a non-empty ``expected`` secret was configured and ``provided``
    matches it. Empty ``expected`` → False (fail closed). Constant-time comparison."""
    if not expected:
        return False
    return hmac.compare_digest(str(provided or ""), str(expected))


def verify_stripe_signature(
    payload: bytes, sig_header: str, secret: str, *, tolerance: int = 300, now: float | None = None
) -> bool:
    """Verify a Stripe ``Stripe-Signature`` header against the raw request body.

    Mirrors Stripe's scheme: the header is ``t=<ts>,v1=<sig>[,v1=...]`` and each ``v1``
    is ``HMAC_SHA256(secret, f"{ts}.{payload}")``. Returns False on any malformation,
    signature mismatch, or timestamp outside ``tolerance`` seconds (replay guard)."""
    if not secret or not sig_header:
        return False
    parts = {}
    for item in sig_header.split(","):
        key, _, value = item.partition("=")
        key = key.strip()
        if key == "v1":
            parts.setdefault("v1", []).append(value.strip())
        elif key:
            parts[key] = value.strip()
    timestamp = parts.get("t")
    signatures = parts.get("v1", [])
    if not timestamp or not signatures:
        return False
    try:
        ts = int(timestamp)
    except (TypeError, ValueError):
        return False
    current = time.time() if now is None else now
    if abs(current - ts) > tolerance:
        return False
    signed_payload = f"{timestamp}.".encode() + payload
    expected = hmac.new(secret.encode(), signed_payload, hashlib.sha256).hexdigest()
    return any(hmac.compare_digest(expected, candidate) for candidate in signatures)
