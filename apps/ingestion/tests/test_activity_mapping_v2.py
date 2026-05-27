"""Mapping coverage for the expanded activity set (endurance/outdoor, fitness, culture)."""

from apps.ingestion.mapping import match_element
from apps.ingestion.sources.overture import match_overture


def _slugs(tags):
    return {slug for slug, _, _ in match_element(tags)}


def test_running_track_and_sport():
    assert "running" in _slugs({"leisure": "track"})
    assert "running" in _slugs({"sport": "running"})


def test_cycling_and_hiking():
    assert "cycling" in _slugs({"sport": "cycling"})
    assert "hiking" in _slugs({"route": "hiking"})


def test_swimming_and_climbing():
    assert "swimming" in _slugs({"leisure": "swimming_pool"})
    assert "climbing" in _slugs({"sport": "climbing"})


def test_team_sport_pitches():
    assert "volleyball" in _slugs({"leisure": "pitch", "sport": "volleyball"})
    assert "handball" in _slugs({"leisure": "pitch", "sport": "handball"})


def test_culture_venues():
    assert "museum_visit" in _slugs({"tourism": "museum"})
    assert "theatre_show" in _slugs({"amenity": "theatre"})


def test_park_includes_running_and_cycling():
    slugs = _slugs({"leisure": "park"})
    assert {"running", "cycling"} <= slugs


def test_overture_new_categories():
    assert any(s == "group_fitness" for s, _, _ in match_overture("gym"))
    assert any(s == "museum_visit" for s, _, _ in match_overture("art_museum"))
    assert any(s == "hiking" for s, _, _ in match_overture("hiking_trail"))
