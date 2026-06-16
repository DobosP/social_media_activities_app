"""Taxonomy services: the shared category-ancestry walk (extracted for W3-F2 so the
recommendations embedding and the child category-allowlist gate can't drift)."""

import pytest

from apps.taxonomy.models import ActivityCategory, ActivityType
from apps.taxonomy.services import category_ancestry_slugs

pytestmark = pytest.mark.django_db


def test_category_ancestry_slugs_walks_parents_nearest_first():
    top = ActivityCategory.objects.create(slug="tx-sport", name="Sport")
    mid = ActivityCategory.objects.create(slug="tx-ball", name="Ball", parent=top)
    t = ActivityType.objects.create(
        slug="tx-bball", name="Basketball", category=mid, is_active=True
    )
    assert category_ancestry_slugs(t) == ["tx-ball", "tx-sport"]


def test_category_ancestry_slugs_empty_without_category():
    # Defensive None branch (the FK is non-null in the schema, so use a duck-typed stand-in).
    class _Typeless:
        category = None

    assert category_ancestry_slugs(_Typeless()) == []
