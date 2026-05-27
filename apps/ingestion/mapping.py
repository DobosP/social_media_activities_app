"""OSM-tag -> activity-type mapping rules.

Pure Python (no Django) so it can be unit-tested in isolation. `match_element`
takes a place's OSM tag dict and returns the activities it implies.
"""

from dataclasses import dataclass


@dataclass(frozen=True)
class TagRule:
    rule_id: str
    match: dict  # every key/value must equal the element's tag
    activity_slug: str
    confidence: float = 0.7


# Specific rules: a place matching `match` supports `activity_slug`.
MAPPING: list[TagRule] = [
    # Basketball
    TagRule("bball_pitch", {"leisure": "pitch", "sport": "basketball"}, "basketball", 0.9),
    TagRule("bball_sport", {"sport": "basketball"}, "basketball", 0.7),
    # Football
    TagRule("football_pitch", {"leisure": "pitch", "sport": "soccer"}, "football", 0.9),
    TagRule("football_sport", {"sport": "soccer"}, "football", 0.7),
    TagRule("futsal_pitch", {"leisure": "pitch", "sport": "futsal"}, "football", 0.85),
    # Table tennis / ping pong
    TagRule("tt_pitch", {"leisure": "pitch", "sport": "table_tennis"}, "table_tennis", 0.9),
    TagRule("tt_sport", {"sport": "table_tennis"}, "table_tennis", 0.8),
    TagRule("tt_table", {"amenity": "table_tennis_table"}, "table_tennis", 0.9),
    # Tennis
    TagRule("tennis_pitch", {"leisure": "pitch", "sport": "tennis"}, "tennis", 0.9),
    # Reading
    TagRule("library", {"amenity": "library"}, "reading", 0.95),
    TagRule("books_shop", {"shop": "books"}, "reading", 0.6),
    # Board games
    TagRule("games_shop", {"shop": "games"}, "board_games", 0.7),
    TagRule("boardgames_shop", {"shop": "boardgames"}, "board_games", 0.85),
    TagRule("cafe_boardgames", {"amenity": "cafe", "board_games": "yes"}, "board_games", 0.8),
    # Video games
    TagRule("arcade", {"leisure": "amusement_arcade"}, "video_games", 0.8),
    TagRule("video_shop", {"shop": "video_games"}, "video_games", 0.7),
    TagRule("internet_cafe", {"amenity": "internet_cafe"}, "video_games", 0.5),
]

# Venues that imply several activities but name no specific sport. Emitted at
# low confidence as candidates (not ground truth); cleaned up later via
# user-confirmation (PlaceActivity.origin/confidence).
GENERIC_VENUES: dict[str, tuple[dict, list[str], float]] = {
    "sports_centre": (
        {"leisure": "sports_centre"},
        ["basketball", "football", "table_tennis", "tennis"],
        0.3,
    ),
    "community_centre": ({"amenity": "community_centre"}, ["board_games", "reading"], 0.3),
    "playground": ({"leisure": "playground"}, ["football", "basketball"], 0.2),
    # Parks host casual outdoor games (street ball, kickabouts, park chess tables).
    "park": ({"leisure": "park"}, ["football", "basketball", "streetball", "chess"], 0.2),
    "arts_centre": ({"amenity": "arts_centre"}, ["board_games", "reading"], 0.25),
}


def _matches(tags: dict, criteria: dict) -> bool:
    return all(tags.get(key) == value for key, value in criteria.items())


def match_element(tags: dict) -> list[tuple[str, str, float]]:
    """Return [(activity_slug, rule_id, confidence)], de-duped per activity
    keeping the highest-confidence rule that fired."""
    best: dict[str, tuple[str, float]] = {}

    def offer(slug: str, rule_id: str, confidence: float) -> None:
        current = best.get(slug)
        if current is None or confidence > current[1]:
            best[slug] = (rule_id, confidence)

    for rule in MAPPING:
        if _matches(tags, rule.match):
            offer(rule.activity_slug, rule.rule_id, rule.confidence)

    for rule_id, (criteria, slugs, confidence) in GENERIC_VENUES.items():
        if _matches(tags, criteria):
            for slug in slugs:
                offer(slug, rule_id, confidence)

    return [(slug, rule_id, confidence) for slug, (rule_id, confidence) in best.items()]
