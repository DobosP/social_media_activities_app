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


def test_archive_maps_to_archive():
    assert _by_slug({"amenity": "archive", "name": "National Archives"})["archive"] == 0.95


def test_secondhand_bookshop_is_reading_and_used_books():
    # A second-hand bookshop supports both general reading and the used-books type.
    result = _by_slug({"shop": "books", "second_hand": "only"})
    assert result["used_bookshop"] == 0.9
    assert result["reading"] == 0.6


def test_antiquarian_books_tag():
    assert _by_slug({"shop": "books", "books": "antiquarian"})["used_bookshop"] == 0.9


def test_public_bookcase_low_confidence_reading():
    assert _by_slug({"amenity": "public_bookcase"})["reading"] == 0.4
