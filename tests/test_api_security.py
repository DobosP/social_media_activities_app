"""Cross-cutting API security configuration checks.

Asserts the *configuration* rather than exercising live throttling, so it can't
leak throttle state through the shared cache into other tests.
"""

from django.conf import settings


def test_throttling_configured_in_base():
    from config.settings import base

    classes = base.REST_FRAMEWORK["DEFAULT_THROTTLE_CLASSES"]
    assert "rest_framework.throttling.AnonRateThrottle" in classes
    assert "rest_framework.throttling.UserRateThrottle" in classes
    rates = base.REST_FRAMEWORK["DEFAULT_THROTTLE_RATES"]
    assert rates["anon"] and rates["user"]


def test_throttling_disabled_in_tests():
    # Deterministic tests: throttling is off under config.settings.test.
    assert settings.REST_FRAMEWORK["DEFAULT_THROTTLE_CLASSES"] == []


def test_prod_security_hardening():
    from config.settings import prod

    assert prod.SECURE_HSTS_PRELOAD is True
    assert prod.SESSION_COOKIE_SECURE is True
    assert prod.CSRF_COOKIE_SECURE is True
    assert prod.SECURE_PROXY_SSL_HEADER == ("HTTP_X_FORWARDED_PROTO", "https")
