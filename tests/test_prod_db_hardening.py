"""Production DB resilience config (threat model finding #2)."""

from config.settings import prod


def test_statement_timeout_configured():
    opts = prod.DATABASES["default"]["OPTIONS"]
    assert "statement_timeout" in opts["options"]


def test_connection_reuse_and_health_checks():
    db = prod.DATABASES["default"]
    assert db["CONN_MAX_AGE"] >= 0
    assert db["CONN_HEALTH_CHECKS"] is True


def test_postgis_engine_forced():
    assert prod.DATABASES["default"]["ENGINE"] == "django.contrib.gis.db.backends.postgis"
