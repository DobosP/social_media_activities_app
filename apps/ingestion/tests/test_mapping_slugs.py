"""Regression: every activity slug referenced by the OSM mapping rules must exist in the
seeded taxonomy — an alias listed as a slug crashes `ingest_places` at run time (the
'streetball' park-rule bug, found live 2026-07-07)."""

import pytest

from apps.ingestion.mapping import GENERIC_VENUES, MAPPING
from apps.taxonomy.models import ActivityType

pytestmark = pytest.mark.django_db


def test_every_mapping_slug_exists_in_taxonomy():
    known = set(ActivityType.objects.values_list("slug", flat=True))
    referenced = {rule.activity_slug for rule in MAPPING}
    referenced |= {slug for _tags, slugs, _conf in GENERIC_VENUES.values() for slug in slugs}
    missing = referenced - known
    assert not missing, f"mapping references unknown activity slugs: {sorted(missing)}"
