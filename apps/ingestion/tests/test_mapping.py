from apps.ingestion.mapping import match_element


def _by_slug(tags):
    return {slug: conf for slug, _rule, conf in match_element(tags)}


def test_basketball_pitch_high_confidence():
    assert _by_slug({"leisure": "pitch", "sport": "basketball"})["basketball"] == 0.9


def test_library_maps_to_reading_only():
    result = match_element({"amenity": "library", "name": "City Library"})
    assert [slug for slug, _, _ in result] == ["reading"]


def test_generic_sports_centre_is_low_confidence_multi():
    result = _by_slug({"leisure": "sports_centre"})
    assert set(result) == {"basketball", "football", "table_tennis", "tennis"}
    assert all(conf == 0.3 for conf in result.values())


def test_dedup_keeps_highest_confidence():
    # sport=basketball (0.7) and pitch+basketball (0.9) -> keep 0.9.
    assert _by_slug({"leisure": "pitch", "sport": "basketball"})["basketball"] == 0.9


def test_unmapped_returns_empty():
    assert match_element({"amenity": "bank"}) == []


def test_cafe_requires_board_games_tag():
    assert match_element({"amenity": "cafe"}) == []
    assert _by_slug({"amenity": "cafe", "board_games": "yes"})["board_games"] == 0.8
