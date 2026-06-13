"""F19 — crowd venue facts + kid-suitability facts (ingest-safe overlay).

OSM tags are read FIRST; crowd votes (quorum=3) fill in only where OSM is silent. Counts-only,
no voter identity, never a composite "safe for kids" score, never written back to Place.
"""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceFactVote
from apps.places.services import (
    NotEligible,
    PlacesError,
    fact_vote_summary,
    has_kid_facts,
    place_fact_status,
    venue_facts,
    venue_facts_detail,
    vote_on_fact,
)

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
FK = PlaceFactVote.FactKey


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if band == AgeBand.UNDER_16:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _place(raw_tags=None, source=Place.Source.OSM):
    return Place.objects.create(name="V", location=PT, source=source, raw_tags=raw_tags or {})


# --- OSM-first derivation -------------------------------------------------------------


def test_osm_kv_facts():
    assert place_fact_status(_place({"drinking_water": "yes"}), FK.DRINKING_WATER) == "true"
    assert place_fact_status(_place({"toilets": "no"}), FK.TOILETS) == "false"
    assert place_fact_status(_place({"lit": "yes"}), FK.LIT_AT_NIGHT) == "true"


def test_osm_present_facts():
    assert place_fact_status(_place({"leisure": "playground"}), FK.PLAYGROUND) == "true"
    assert place_fact_status(_place({"barrier": "fence"}), FK.FENCED) == "true"
    assert place_fact_status(_place({"natural": "tree"}), FK.SHADE) == "true"
    # Absence of the tag is NOT a 'no' — it's unknown (then crowd can fill it).
    assert place_fact_status(_place({}), FK.PLAYGROUND) == "unknown"


def test_indoor_shelter_has_no_osm_source():
    assert place_fact_status(_place({"building": "yes"}), FK.INDOOR_SHELTER) == "unknown"


# --- crowd overlay (only where OSM is silent) -----------------------------------------


def test_crowd_quorum_confirms_and_disputes():
    place = _place({})  # OSM silent on indoor_shelter
    for i in range(3):
        vote_on_fact(_user(f"y{i}"), place, FK.INDOOR_SHELTER, True)
    assert place_fact_status(place, FK.INDOOR_SHELTER) == "true"
    place2 = _place({})
    for i in range(3):
        vote_on_fact(_user(f"n{i}"), place2, FK.INDOOR_SHELTER, False)
    assert place_fact_status(place2, FK.INDOOR_SHELTER) == "false"


def test_crowd_subquorum_and_tie_stay_unknown():
    place = _place({})
    vote_on_fact(_user("s1"), place, FK.INDOOR_SHELTER, True)
    vote_on_fact(_user("s2"), place, FK.INDOOR_SHELTER, True)
    assert place_fact_status(place, FK.INDOOR_SHELTER) == "unknown"  # 2 < quorum
    # Make it 3 yes / 3 no -> tie -> unknown.
    vote_on_fact(_user("s3"), place, FK.INDOOR_SHELTER, True)
    for i in range(3):
        vote_on_fact(_user(f"sn{i}"), place, FK.INDOOR_SHELTER, False)
    assert place_fact_status(place, FK.INDOOR_SHELTER) == "unknown"


def test_osm_wins_over_crowd():
    # OSM says drinking water = no; even 3 crowd 'yes' votes don't override map data.
    place = _place({"drinking_water": "no"})
    for i in range(3):
        vote_on_fact(_user(f"dw{i}"), place, FK.DRINKING_WATER, True)
    assert place_fact_status(place, FK.DRINKING_WATER) == "false"


# --- vote gating + idempotency --------------------------------------------------------


def test_vote_requires_participation():
    place = _place({})
    unverified = User.objects.create_user(username="u0", password="pw")
    with pytest.raises(NotEligible):
        vote_on_fact(unverified, place, FK.TOILETS, True)


def test_vote_rejects_unknown_fact():
    with pytest.raises(PlacesError):
        vote_on_fact(_user("uf"), _place({}), "not_a_real_fact", True)


def test_vote_rejects_nonpublic_place():
    # A USER-source place with no published proposal is not public_places() -> can't be voted on.
    place = _place(source=Place.Source.USER)
    with pytest.raises(PlacesError):
        vote_on_fact(_user("np"), place, FK.TOILETS, True)


def test_vote_is_idempotent_and_mind_change_updates():
    place = _place({})
    voter = _user("mc")
    vote_on_fact(voter, place, FK.TOILETS, True)
    vote_on_fact(voter, place, FK.TOILETS, True)  # repeat -> still one row
    vote_on_fact(voter, place, FK.TOILETS, False)  # mind change -> updates the same row
    rows = PlaceFactVote.objects.filter(place=place, user=voter, fact_key=FK.TOILETS)
    assert rows.count() == 1
    assert rows.first().value is False


# --- summary / display / privacy ------------------------------------------------------


def test_summary_counts_and_own_vote_only():
    place = _place({})
    a, b = _user("sa"), _user("sb")
    vote_on_fact(a, place, FK.TOILETS, True)
    vote_on_fact(b, place, FK.TOILETS, False)
    summary = fact_vote_summary(place, FK.TOILETS, viewer=a)
    assert summary["yes"] == 1 and summary["no"] == 1
    assert summary["my_vote"] is True
    # No voter identity is ever exposed.
    assert "voters" not in summary and "users" not in summary


def test_venue_facts_lists_all_keys_with_state():
    rows = venue_facts(_place({"toilets": "yes"}))
    keys = {r["key"] for r in rows}
    assert keys == set(FK.values)
    toilets = next(r for r in rows if r["key"] == "toilets")
    assert toilets["state"] == "true"


def test_detail_marks_osm_sourced():
    place = _place({"toilets": "yes"})
    rows = {r["key"]: r for r in venue_facts_detail(place, viewer=None)}
    assert rows["toilets"]["osm_sourced"] is True
    assert rows["indoor_shelter"]["osm_sourced"] is False


def test_kid_badge_is_soft():
    # A confirmed kid-relevant fact lights the badge; a venue with only unknowns is never excluded.
    place = _place({"toilets": "yes"})
    assert has_kid_facts(place) is True
    assert has_kid_facts(_place({})) is False  # unknowns -> no badge, but still a valid place


def test_kid_badge_lights_on_crowd_quorum():
    # The soft kid badge must also light when a KID-relevant fact reaches quorum via CROWD votes
    # (not only via OSM data).
    place = _place({})  # OSM silent on 'fenced'
    assert has_kid_facts(place) is False
    for i in range(3):
        vote_on_fact(_user(f"kb{i}"), place, FK.FENCED, True)
    assert has_kid_facts(place) is True


def test_vote_rate_limit_enforced(settings):
    settings.FACT_VOTE_RATE_LIMIT = 1
    settings.FACT_VOTE_RATE_WINDOW_SECONDS = 3600
    place = _place({})
    voter = _user("rl")
    vote_on_fact(voter, place, FK.TOILETS, True)  # first is allowed
    with pytest.raises(PlacesError):
        vote_on_fact(voter, place, FK.SHADE, True)  # second (same action bucket) is throttled


def test_not_written_back_to_place():
    place = _place({})
    vote_on_fact(_user("wb"), place, FK.TOILETS, True)
    place.refresh_from_db()
    assert place.raw_tags == {}  # the overlay never mutates Place/raw_tags (re-ingest safe)


def test_covoting_is_not_a_shared_activity():
    # Pinned invariant: co-voting on a venue must NEVER count as a shared activity / enable connect.
    from apps.connections.services import shares_activity

    place = _place({})
    a, b = _user("ca"), _user("cb")
    vote_on_fact(a, place, FK.TOILETS, True)
    vote_on_fact(b, place, FK.TOILETS, True)
    assert shares_activity(a, b) is False
