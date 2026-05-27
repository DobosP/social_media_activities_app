"""The OpenAPI schema (the client-facing contract) generates and is served."""

import pytest
from rest_framework.test import APIClient

pytestmark = pytest.mark.django_db


def test_schema_generates_and_is_served():
    resp = APIClient().get("/api/schema/")
    assert resp.status_code == 200
    body = resp.content.decode()
    assert "openapi:" in body  # YAML schema document


def test_swagger_ui_served():
    assert APIClient().get("/api/docs/").status_code == 200


def test_schema_has_curated_tags(settings):
    names = {t["name"] for t in settings.SPECTACULAR_SETTINGS["TAGS"]}
    # A few representative domains must be documented.
    assert {"places", "social", "safety", "donations"} <= names
