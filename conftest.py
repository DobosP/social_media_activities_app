"""Project-wide pytest fixtures.

Several tests run as ``TransactionTestCase`` (``@pytest.mark.django_db(transaction=True)``
— e.g. the WebSocket consumer tests), which **flushes every table** at teardown,
including the taxonomy that data migrations seed at database creation. Under
pytest's ``--reuse-db`` (the CI default) that seed is not restored, so any later
test that relies on the migration-seeded taxonomy fails depending on collection
order. This autouse fixture re-applies the *idempotent* seed migrations for any
test that touches the database, making the suite order-independent and CI
deterministic.
"""

import importlib

import pytest

# Applied in dependency order (0004/0005 look up categories/types created earlier).
_SEED_MODULES = (
    "apps.taxonomy.migrations.0002_seed_taxonomy",
    "apps.taxonomy.migrations.0004_seed_activities_v2",
    "apps.taxonomy.migrations.0005_seed_reading_archives",
    # W2-F1: additive RO search aliases (incl. 'fotbal' on football) — must re-apply AFTER 0002
    # re-creates football, or a transaction-test flush would leave it without the RO alias.
    "apps.taxonomy.migrations.0006_seed_ro_aliases",
    # F9: the child-safe venue-class allowlist (library/park/...) the venue gate reads.
    "apps.places.migrations.0007_seed_child_venue_classes",
)


def _reseed_taxonomy():
    from django.apps import apps as global_apps

    from apps.places.models import ChildVenueClass
    from apps.taxonomy.models import ActivityType

    # Fast path: every seed is intact, so do nothing but a few cheap lookups. The football/fotbal
    # check also catches a DB migrated before W2-F1's 0006 (under --reuse-db) so the RO alias is
    # re-applied without a full --create-db.
    if (
        ActivityType.objects.filter(slug="basketball").exists()
        and ActivityType.objects.filter(slug="archive").exists()
        and ChildVenueClass.objects.filter(key="library").exists()
        and ActivityType.objects.filter(slug="football", aliases__contains=["fotbal"]).exists()
    ):
        return
    for path in _SEED_MODULES:
        importlib.import_module(path).seed(global_apps, None)


@pytest.fixture(autouse=True)
def _ensure_taxonomy_seed(request):
    """Reseed the taxonomy for DB tests if a prior transaction test flushed it."""
    if "django_db" not in request.keywords:
        return
    # Make sure the database fixture is set up before we touch the DB.
    if "transactional_db" in request.fixturenames:
        request.getfixturevalue("transactional_db")
    else:
        request.getfixturevalue("db")
    _reseed_taxonomy()


@pytest.fixture(autouse=True)
def _reset_circuit_breakers():
    """Isolate the in-process provider CircuitBreaker registry between tests. It is keyed on the
    PRODUCTION 'stripe'/'booking' keys, so a provider-failure test could otherwise leave a breaker
    near/at its threshold and make a LATER provider test fast-fail (ProviderUnavailable) before it
    makes any HTTP call — an order-dependent flake (esp. under pytest-randomly). Cheap clear."""
    from apps.ops.resilience import CircuitBreaker

    CircuitBreaker._registry.clear()
    yield
    CircuitBreaker._registry.clear()
