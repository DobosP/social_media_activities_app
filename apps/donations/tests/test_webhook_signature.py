"""W1-12: webhook authentication — constant-time shared secret (fail-closed) and Stripe
signature verification with a replay window. See docs/PRODUCTION_HARDENING_PLAN_2026-05.md."""

import hashlib
import hmac

from apps.donations.webhooks import constant_time_secret_ok, verify_stripe_signature


def _stripe_header(payload: bytes, secret: str, ts: int) -> str:
    signed = f"{ts}.".encode() + payload
    v1 = hmac.new(secret.encode(), signed, hashlib.sha256).hexdigest()
    return f"t={ts},v1={v1}"


def test_constant_time_secret_fails_closed():
    assert constant_time_secret_ok("x", "x") is True
    assert constant_time_secret_ok("x", "y") is False
    assert constant_time_secret_ok("x", "") is False  # no secret configured → reject
    assert constant_time_secret_ok("", "") is False


def test_valid_stripe_signature_accepted():
    payload = b'{"id":"evt_1"}'
    secret = "whsec_test"
    ts = 1_700_000_000
    header = _stripe_header(payload, secret, ts)
    assert verify_stripe_signature(payload, header, secret, now=ts) is True


def test_tampered_payload_rejected():
    payload = b'{"id":"evt_1"}'
    secret = "whsec_test"
    ts = 1_700_000_000
    header = _stripe_header(payload, secret, ts)
    assert verify_stripe_signature(b'{"id":"forged"}', header, secret, now=ts) is False


def test_wrong_secret_rejected():
    payload = b"{}"
    ts = 1_700_000_000
    header = _stripe_header(payload, "real", ts)
    assert verify_stripe_signature(payload, header, "attacker", now=ts) is False


def test_stale_timestamp_rejected():
    payload = b"{}"
    secret = "whsec_test"
    ts = 1_700_000_000
    header = _stripe_header(payload, secret, ts)
    # 10k seconds later — outside the default 300s tolerance (replay guard).
    assert verify_stripe_signature(payload, header, secret, now=ts + 10_000) is False


def test_missing_pieces_rejected():
    assert verify_stripe_signature(b"{}", "", "secret") is False
    assert verify_stripe_signature(b"{}", "t=1", "secret", now=1) is False  # no v1
    assert verify_stripe_signature(b"{}", "v1=abc", "secret") is False  # no timestamp
