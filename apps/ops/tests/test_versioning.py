"""API versioning: the surface is mounted under the canonical /api/v1/ AND a backward-compatible
unversioned /api/ alias; both resolve identically, and the schema documents only /api/v1/."""

import pytest
from rest_framework.test import APIClient

pytestmark = pytest.mark.django_db


def test_v1_and_unversioned_alias_both_resolve():
    client = APIClient()
    # A public AllowAny list endpoint (taxonomy categories) under BOTH prefixes.
    v1 = client.get("/api/v1/taxonomy/categories/")
    alias = client.get("/api/taxonomy/categories/")
    assert v1.status_code == 200
    assert alias.status_code == 200
    assert v1.json() == alias.json()  # identical view, identical response


def test_v1_path_with_explicit_signature_view_does_not_500():
    # A view with an explicit method signature (no **kwargs) must work under /api/v1/ — proves the
    # literal "v1" segment does NOT leak a `version` kwarg into handlers (which would TypeError).
    client = APIClient()
    resp = client.get("/api/v1/media/file/not-a-real-token/")
    assert resp.status_code in (401, 403, 404)  # a clean auth/validation response, never a 500


def test_schema_documents_v1_not_the_unversioned_alias():
    client = APIClient()
    resp = client.get("/api/schema/?format=json")
    assert resp.status_code == 200
    paths = resp.json().get("paths", {})
    assert paths, "schema should have paths"
    # The bare /api/ alias is NOT documented (no duplicate operationIds)...
    assert not any(p.startswith("/api/") and not p.startswith("/api/v1/") for p in paths), sorted(
        paths
    )[:8]
    # ...the canonical versioned surface IS...
    assert any(p.startswith("/api/v1/") for p in paths)
    # ...and the root ops probes are retained in the contract (not collateral-dropped by the hook).
    assert "/healthz" in paths and "/readyz" in paths
