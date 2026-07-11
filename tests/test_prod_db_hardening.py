"""Production DB resilience config (threat model finding #2)."""

import pytest
from django.core.exceptions import ImproperlyConfigured

from config.settings import prod


def test_statement_timeout_configured():
    opts = prod.DATABASES["default"]["OPTIONS"]
    assert "statement_timeout" in opts["options"]


def test_asgi_uses_bounded_psycopg_pool():
    db = prod.DATABASES["default"]
    assert db["CONN_MAX_AGE"] == 0
    assert db["CONN_HEALTH_CHECKS"] is True
    assert db["OPTIONS"]["pool"] == {"min_size": 0, "max_size": 4, "timeout": 10.0}


def test_pool_options_are_env_tunable(monkeypatch):
    monkeypatch.setattr(prod, "DB_POOL_MIN_SIZE", 1)
    monkeypatch.setattr(prod, "DB_POOL_MAX_SIZE", 3)
    monkeypatch.setattr(prod, "DB_POOL_TIMEOUT", 2.5)

    assert prod._database_pool_options() == {"min_size": 1, "max_size": 3, "timeout": 2.5}


@pytest.mark.parametrize(
    ("minimum", "maximum", "timeout"),
    [
        (-1, 4, 10.0),
        (0, 0, 10.0),
        (5, 4, 10.0),
        (0, 4, 0.0),
        (0, 4, float("nan")),
        (0, 4, float("inf")),
    ],
)
def test_invalid_pool_options_fail_closed(monkeypatch, minimum, maximum, timeout):
    monkeypatch.setattr(prod, "DB_POOL_MIN_SIZE", minimum)
    monkeypatch.setattr(prod, "DB_POOL_MAX_SIZE", maximum)
    monkeypatch.setattr(prod, "DB_POOL_TIMEOUT", timeout)

    with pytest.raises(ImproperlyConfigured):
        prod._database_pool_options()


def test_postgis_engine_forced():
    assert prod.DATABASES["default"]["ENGINE"] == "django.contrib.gis.db.backends.postgis"
