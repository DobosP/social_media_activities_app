"""F15: accessibility facts derived honestly from OSM tags + the per-user access preference."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.places.services import (
    accessibility_facts,
    get_access_preference,
    matches_access_preference,
    set_access_preference,
)

pytestmark = pytest.mark.django_db


def _place(raw_tags):
    return Place.objects.create(
        name="Venue",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        raw_tags=raw_tags,
    )


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_facts_mapped_from_osm_tags():
    facts = accessibility_facts(
        _place({"wheelchair": "yes", "toilets:wheelchair": "no", "changing_table": "yes"})
    )
    assert facts["step_free"] == "true"
    assert facts["accessible_toilet"] == "false"
    assert facts["changing_table"] == "true"
    assert facts["tactile_paving"] == "unknown"  # tag absent


def test_limited_value_maps_to_limited():
    assert accessibility_facts(_place({"wheelchair": "limited"}))["step_free"] == "limited"


def test_all_unknown_when_absent_or_enrichment_only():
    assert all(v == "unknown" for v in accessibility_facts(_place({})).values())
    # An enrichment-only raw_tags (namespaced) must NOT be read as accessibility facts.
    enriched = accessibility_facts(_place({"google": {"place_id": "g1"}}))
    assert all(v == "unknown" for v in enriched.values())


def test_unrecognised_value_is_unknown_never_true():
    # Honesty: an odd OSM value must never be claimed as accessible.
    assert accessibility_facts(_place({"wheelchair": "designated"}))["step_free"] == "unknown"


def test_matches_preference_soft_classifier():
    yes = accessibility_facts(_place({"wheelchair": "yes"}))
    no = accessibility_facts(_place({"wheelchair": "no"}))
    unknown = accessibility_facts(_place({}))

    class Pref:
        needs_step_free = True
        needs_accessible_toilet = False

    pref = Pref()
    assert matches_access_preference(yes, pref) == "match"
    assert matches_access_preference(no, pref) == "mismatch"
    assert matches_access_preference(unknown, pref) == "unknown"  # never excludes
    assert matches_access_preference(yes, None) == "unknown"  # no preference → no badge


def test_set_and_get_preference():
    u = _user("prefuser")
    assert get_access_preference(u) is None
    set_access_preference(
        u, needs_step_free=True, needs_accessible_toilet=False, prefers_quiet=True
    )
    pref = get_access_preference(u)
    assert pref.needs_step_free is True
    assert pref.prefers_quiet is True
    # update_or_create overwrites the single row.
    set_access_preference(u, needs_step_free=False)
    assert get_access_preference(u).needs_step_free is False
