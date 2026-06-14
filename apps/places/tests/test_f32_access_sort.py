"""F32 — richer accessibility facts (hearing_loop, automatic_door) + a needs-aware SOFT sort.

The sort stably floats venues that CONFIRM the viewer's stated needs to the top and NEVER hides
an unknown-accessibility venue (a nudge, not a filter). prefers_quiet is deliberately not wired.
"""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import AccessPreference, Place
from apps.places.services import (
    accessibility_facts,
    matches_access_preference,
    set_access_preference,
    sort_by_access_match,
)

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _place(name, raw_tags):
    return Place.objects.create(
        name=name,
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        raw_tags=raw_tags,
    )


# --- richer facts ----------------------------------------------------------------------------


def test_new_clean_osm_facts_are_derived():
    facts = accessibility_facts(_place("V", {"hearing_loop": "yes", "automatic_door": "no"}))
    assert facts["hearing_loop"] == "true"
    assert facts["automatic_door"] == "false"


def test_non_binary_automatic_door_value_is_unknown_not_true():
    # "button"/"motion" are real OSM values but not a clean yes — fail-closed to unknown.
    facts = accessibility_facts(_place("V", {"automatic_door": "button"}))
    assert facts["automatic_door"] == "unknown"


def test_hearing_loop_participates_in_the_soft_match():
    p = AccessPreference(needs_hearing_loop=True)
    assert matches_access_preference({"hearing_loop": "true"}, p) == "match"
    assert matches_access_preference({"hearing_loop": "false"}, p) == "mismatch"
    assert matches_access_preference({"hearing_loop": "unknown"}, p) == "unknown"


# --- the needs-aware sort (nudge, never hide) ------------------------------------------------


def test_sort_floats_matches_first_and_hides_nothing():
    pref = AccessPreference(needs_step_free=True)
    match = _place("Match", {"wheelchair": "yes"})
    unknown = _place("Unknown", {})
    mismatch = _place("Mismatch", {"wheelchair": "no"})
    # Incoming order: unknown, mismatch, match (e.g. alphabetical/distance).
    ordered = sort_by_access_match([unknown, mismatch, match], pref)
    assert ordered[0] is match  # the confirmed match floats up
    assert set(ordered) == {match, unknown, mismatch}  # NOTHING is dropped
    # within the non-matching group the original relative order is preserved (stable)
    assert ordered.index(unknown) < ordered.index(mismatch)


def test_sort_is_a_noop_without_a_stated_need():
    pref = AccessPreference()  # no need set
    a, b = _place("A", {"wheelchair": "yes"}), _place("B", {})
    assert sort_by_access_match([b, a], pref) == [b, a]  # order untouched
    assert sort_by_access_match([b, a], None) == [b, a]  # anonymous: untouched


def test_prefers_quiet_alone_never_reorders():
    pref = AccessPreference(prefers_quiet=True)  # no OSM-backed need
    a, b = _place("A", {"wheelchair": "yes"}), _place("B", {})
    assert sort_by_access_match([b, a], pref) == [b, a]


# --- web surfaces ----------------------------------------------------------------------------


def test_places_list_marks_and_floats_matches_for_a_member():
    user = _user("f32_member")
    set_access_preference(user, needs_step_free=True)
    _place("ZZZ Step-free hall", {"wheelchair": "yes"})  # last alphabetically...
    _place("AAA Unknown hall", {})  # ...first alphabetically
    c = Client()
    c.force_login(user)
    html = c.get("/places/list/").content.decode()
    # Both still listed (nothing hidden), and the match carries the marker.
    assert "ZZZ Step-free hall" in html and "AAA Unknown hall" in html
    assert "Matches your access needs" in html
    # The step-free venue is floated ABOVE the unknown one despite losing the alphabetical order.
    assert html.index("ZZZ Step-free hall") < html.index("AAA Unknown hall")


def test_places_list_unaffected_for_anonymous_viewer():
    _place("Step-free hall", {"wheelchair": "yes"})
    html = Client().get("/places/list/").content.decode()
    assert "Matches your access needs" not in html


def test_access_form_saves_hearing_loop_need():
    user = _user("f32_pref")
    c = Client()
    c.force_login(user)
    resp = c.post("/access/", {"needs_hearing_loop": "on"})
    assert resp.status_code == 302
    assert AccessPreference.objects.get(user=user).needs_hearing_loop is True


def test_nearme_api_floats_matches_without_hiding():
    from rest_framework.test import APIClient

    user = _user("f32_nearme")
    set_access_preference(user, needs_step_free=True)
    # Without proximity, NearMe orders by id — create the unknown FIRST so the sort has to move
    # the (later-id) match ahead of it.
    unknown = _place("Unknown hall", {})
    match = _place("Step-free hall", {"wheelchair": "yes"})
    api = APIClient()
    api.force_authenticate(user)
    rows = api.get("/api/discovery/near-me/").json()
    names = [r["name"] for r in rows]
    assert match.name in names and unknown.name in names  # nothing dropped
    assert names.index(match.name) < names.index(unknown.name)  # match floated to the top
