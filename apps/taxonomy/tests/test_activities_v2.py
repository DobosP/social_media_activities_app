import pytest

from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def test_new_categories_seeded():
    for slug in ("outdoor", "fitness", "culture"):
        assert ActivityCategory.objects.filter(slug=slug).exists()


def test_endurance_and_culture_types_seeded():
    for slug in ("running", "marathon", "hiking", "cycling", "festival", "city_day"):
        assert ActivityType.objects.filter(slug=slug).exists()


def test_wellness_and_family_traits():
    running = ActivityType.objects.get(slug="running")
    assert running.wellness is True

    hiking = ActivityType.objects.get(slug="hiking")
    assert hiking.wellness is True and hiking.family_friendly is True

    festival = ActivityType.objects.get(slug="festival")
    assert festival.wellness is False and festival.family_friendly is True


def test_existing_types_get_backfilled_traits():
    basketball = ActivityType.objects.get(slug="basketball")
    assert basketball.wellness is True
    assert basketball.family_friendly is True


def test_relations_link_variants_to_base():
    marathon = ActivityType.objects.get(slug="marathon")
    targets = {r.target.slug for r in marathon.relations_out.all()}
    assert "running" in targets


def test_api_exposes_traits():
    from rest_framework.test import APIClient

    resp = APIClient().get("/api/taxonomy/activities/")
    assert resp.status_code == 200
    payload = resp.json()
    rows = payload["results"] if isinstance(payload, dict) else payload
    assert rows and "wellness" in rows[0] and "family_friendly" in rows[0]
