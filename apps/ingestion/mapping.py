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
    TagRule("public_bookcase", {"amenity": "public_bookcase"}, "reading", 0.4),
    # Archives & old papers.
    TagRule("archive", {"amenity": "archive"}, "archive", 0.95),
    # Antiquarian / second-hand bookshops (old papers & rare books).
    TagRule("antiquarian_books", {"shop": "books", "books": "antiquarian"}, "used_bookshop", 0.9),
    TagRule(
        "secondhand_books_only", {"shop": "books", "second_hand": "only"}, "used_bookshop", 0.9
    ),
    TagRule("secondhand_books", {"shop": "books", "second_hand": "yes"}, "used_bookshop", 0.75),
    # Board games
    TagRule("games_shop", {"shop": "games"}, "board_games", 0.7),
    TagRule("boardgames_shop", {"shop": "boardgames"}, "board_games", 0.85),
    TagRule("cafe_boardgames", {"amenity": "cafe", "board_games": "yes"}, "board_games", 0.8),
    # Video games
    TagRule("arcade", {"leisure": "amusement_arcade"}, "video_games", 0.8),
    TagRule("video_shop", {"shop": "video_games"}, "video_games", 0.7),
    TagRule("internet_cafe", {"amenity": "internet_cafe"}, "video_games", 0.5),
    # Running / athletics
    TagRule("running_track", {"leisure": "track"}, "running", 0.7),
    TagRule("athletics", {"sport": "athletics"}, "running", 0.7),
    TagRule("running_sport", {"sport": "running"}, "running", 0.8),
    # Cycling
    TagRule("cycling_sport", {"sport": "cycling"}, "cycling", 0.7),
    TagRule("mtb_sport", {"sport": "mtb"}, "mountain_biking", 0.8),
    # Hiking / outdoor routes
    TagRule("hiking_route", {"route": "hiking"}, "hiking", 0.8),
    TagRule("hiking_sport", {"sport": "hiking"}, "hiking", 0.7),
    # Swimming
    TagRule("swimming_pool", {"leisure": "swimming_pool"}, "swimming", 0.7),
    TagRule("swimming_sport", {"sport": "swimming"}, "swimming", 0.8),
    # Climbing
    TagRule("climbing_sport", {"sport": "climbing"}, "climbing", 0.85),
    # Volleyball / handball / badminton
    TagRule("volleyball_pitch", {"leisure": "pitch", "sport": "volleyball"}, "volleyball", 0.9),
    TagRule("volleyball_sport", {"sport": "volleyball"}, "volleyball", 0.7),
    TagRule("handball_pitch", {"leisure": "pitch", "sport": "handball"}, "handball", 0.9),
    TagRule("handball_sport", {"sport": "handball"}, "handball", 0.7),
    TagRule("badminton_sport", {"sport": "badminton"}, "badminton", 0.8),
    # Fitness & wellness
    TagRule("fitness_centre", {"leisure": "fitness_centre"}, "group_fitness", 0.6),
    TagRule("fitness_sport", {"sport": "fitness"}, "group_fitness", 0.6),
    TagRule("yoga_sport", {"sport": "yoga"}, "yoga", 0.7),
    # Culture & community venues
    TagRule("museum", {"tourism": "museum"}, "museum_visit", 0.8),
    TagRule("theatre", {"amenity": "theatre"}, "theatre_show", 0.8),
    # An art gallery is an exhibition space — the closest seeded culture activity is
    # museum_visit (its aliases already include "exhibition"). Slightly lower than a
    # full museum.
    TagRule("gallery", {"tourism": "gallery"}, "museum_visit", 0.7),
    # A cinema screens films; open_air_cinema is the only seeded film activity and
    # carries the "cinema" alias. Indoor cinemas aren't open-air, so keep confidence
    # modest rather than asserting an exact venue match.
    TagRule("cinema", {"amenity": "cinema"}, "open_air_cinema", 0.5),
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
    # Parks host casual outdoor games and are natural running/cycling spots.
    "park": (
        {"leisure": "park"},
        ["football", "basketball", "streetball", "chess", "running", "cycling"],
        0.2,
    ),
    "nature_reserve": ({"leisure": "nature_reserve"}, ["hiking", "running"], 0.3),
    "arts_centre": ({"amenity": "arts_centre"}, ["workshop", "dance_social", "board_games"], 0.25),
    # Schools host children's sport (gym/pitch) and reading — candidate venues for
    # kids' activities (often available after hours / weekends).
    "school": (
        {"amenity": "school"},
        ["football", "basketball", "volleyball", "table_tennis", "running", "reading"],
        0.2,
    ),
    "college": ({"amenity": "college"}, ["football", "basketball", "running"], 0.2),
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
