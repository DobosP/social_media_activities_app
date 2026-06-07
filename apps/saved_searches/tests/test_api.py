"""F3 DRF: owner-walled viewset, non-int 404, allowlist serializer (no counters)."""

import pytest
from rest_framework.test import APIClient

from apps.saved_searches import services as ss
from apps.saved_searches.models import SavedSearch
from apps.saved_searches.serializers import SavedSearchSerializer

pytestmark = pytest.mark.django_db

BASE = "/api/saved-searches/saved-searches/"


def test_create_via_api_pins_cohort(adult, activity_type):
    c = APIClient()
    c.force_authenticate(adult)
    r = c.post(BASE, {"activity_type": activity_type.id}, format="json")
    assert r.status_code == 201
    assert SavedSearch.objects.get(pk=r.data["id"]).cohort == adult.cohort


def test_create_requires_exactly_one(adult, activity_type, category):
    c = APIClient()
    c.force_authenticate(adult)
    assert c.post(BASE, {}, format="json").status_code == 400  # neither
    both = c.post(BASE, {"activity_type": activity_type.id, "category": category.id}, format="json")
    assert both.status_code == 400


def test_owner_walled_and_non_int_404(adult, adult2, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type)
    other = APIClient()
    other.force_authenticate(adult2)
    assert other.get(f"{BASE}{s.pk}/").status_code == 404
    assert other.delete(f"{BASE}{s.pk}/").status_code in (403, 404)
    assert other.get(f"{BASE}abc/").status_code == 404  # lookup_value_regex
    owner = APIClient()
    owner.force_authenticate(adult)
    assert owner.delete(f"{BASE}{s.pk}/").status_code == 204


def test_serializer_allowlist_no_counters(adult, activity_type):
    s = ss.create_saved_search(adult, activity_type=activity_type, city="Cluj-Napoca")
    data = SavedSearchSerializer(s).data
    forbidden = {
        "match_count",
        "count",
        "n",
        "recipients",
        "last_fired",
        "last_matched",
        "near_you",
        "user",
        "cohort",
    }
    assert forbidden.isdisjoint(data.keys())
    assert not any(k.endswith("_count") or k.endswith("_n") for k in data)
