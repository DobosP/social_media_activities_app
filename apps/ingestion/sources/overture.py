"""Overture Maps places adapter.

Overture publishes a global, normalized places dataset as GeoParquet (~60M POIs)
on a public S3 bucket. We read it with DuckDB's ``read_parquet`` — filtering to a
bounding box, projecting only the columns we need — and normalize each row into a
``RawPlace(source="overture", ...)`` so the ingestion command stays source-agnostic.

DuckDB is imported lazily inside :meth:`_query_rows` so importing this module (and
running the unit tests, which patch that method) never requires the dependency or
network access. The row→``RawPlace`` conversion and the category→activity mapping
are pure functions, unit-tested without DuckDB.
"""

import logging
from collections.abc import Iterator, Sequence

from .base import RawPlace, SourceAdapter

logger = logging.getLogger(__name__)

# Overture's `categories.primary` (and `.alternate`) → our ActivityType slugs.
# Specific category ⇒ one activity at high confidence.
OVERTURE_CATEGORY_MAP: dict[str, tuple[str, float]] = {
    # Reading
    "library": ("reading", 0.95),
    "public_library": ("reading", 0.95),
    "bookstore": ("reading", 0.6),
    "book_store": ("reading", 0.6),
    "used_bookstore": ("reading", 0.6),
    # Basketball
    "basketball_court": ("basketball", 0.9),
    "basketball": ("basketball", 0.8),
    # Football / soccer
    "soccer_field": ("football", 0.9),
    "soccer_club": ("football", 0.7),
    "football": ("football", 0.7),
    "futsal": ("football", 0.85),
    # Tennis
    "tennis_court": ("tennis", 0.9),
    "tennis": ("tennis", 0.8),
    # Table tennis
    "table_tennis": ("table_tennis", 0.85),
    "ping_pong": ("table_tennis", 0.85),
    # Board games
    "board_games": ("board_games", 0.8),
    "board_game_store": ("board_games", 0.85),
    "hobby_shop": ("board_games", 0.4),
    "game_store": ("board_games", 0.5),
    # Video games / arcade
    "arcade": ("video_games", 0.8),
    "video_game_store": ("video_games", 0.7),
    "video_arcade": ("video_games", 0.8),
    "internet_cafe": ("video_games", 0.5),
    # Endurance / outdoor
    "running": ("running", 0.7),
    "track_and_field": ("running", 0.7),
    "hiking_trail": ("hiking", 0.7),
    "trail": ("hiking", 0.5),
    "cycling": ("cycling", 0.7),
    # Fitness & wellness
    "gym": ("group_fitness", 0.6),
    "fitness_center": ("group_fitness", 0.6),
    "fitness_trainer": ("group_fitness", 0.5),
    "yoga_studio": ("yoga", 0.8),
    "swimming_pool": ("swimming", 0.7),
    "rock_climbing": ("climbing", 0.85),
    "climbing_gym": ("climbing", 0.85),
    # Team / racquet
    "volleyball_court": ("volleyball", 0.9),
    "badminton": ("badminton", 0.8),
    # Culture & community
    "museum": ("museum_visit", 0.8),
    "art_museum": ("museum_visit", 0.8),
    "history_museum": ("museum_visit", 0.8),
    "performing_arts_theater": ("theatre_show", 0.8),
    "theater": ("theatre_show", 0.7),
    "concert_hall": ("concert", 0.7),
    "dance_studio": ("dance_social", 0.7),
}

# Generic venues that imply several candidate activities but name no specific one.
# Emitted at low confidence (candidates, not ground truth), like the OSM side.
OVERTURE_GENERIC: dict[str, tuple[list[str], float]] = {
    "sports_club_and_league": (["basketball", "football", "tennis", "table_tennis"], 0.25),
    "stadium_arena": (["basketball", "football"], 0.25),
    "recreation_center": (["basketball", "football", "board_games"], 0.2),
    "community_center": (["board_games", "reading"], 0.3),
    "community_services_non_profit": (["board_games", "reading"], 0.2),
}


def match_overture(
    category: str | None, alternate: Sequence[str] | None = None
) -> list[tuple[str, str, float]]:
    """Map an Overture place's categories to ``[(activity_slug, rule_id, confidence)]``.

    The primary category counts at full confidence; alternates are weaker signals
    (scaled down). De-duped per activity, keeping the highest confidence seen.
    """
    best: dict[str, tuple[str, float]] = {}

    def offer(slug: str, rule_id: str, confidence: float) -> None:
        current = best.get(slug)
        if current is None or confidence > current[1]:
            best[slug] = (rule_id, confidence)

    def consider(cat: str | None, scale: float) -> None:
        if not cat:
            return
        key = cat.strip().lower()
        if key in OVERTURE_CATEGORY_MAP:
            slug, confidence = OVERTURE_CATEGORY_MAP[key]
            offer(slug, f"overture:{key}", round(confidence * scale, 3))
        if key in OVERTURE_GENERIC:
            slugs, confidence = OVERTURE_GENERIC[key]
            for slug in slugs:
                offer(slug, f"overture:{key}", round(confidence * scale, 3))

    consider(category, 1.0)
    for alt in alternate or []:
        consider(alt, 0.7)

    return [(slug, rule_id, confidence) for slug, (rule_id, confidence) in best.items()]


# Columns projected from the Overture places parquet. We use the `bbox` struct's
# centroid for coordinates (numeric, no spatial extension needed to filter/locate).
_SELECT = """
    SELECT
        id,
        names.primary AS name,
        categories.primary AS category,
        categories.alternate AS alternate,
        (bbox.xmin + bbox.xmax) / 2.0 AS lon,
        (bbox.ymin + bbox.ymax) / 2.0 AS lat,
        addresses,
        websites
    FROM read_parquet(?, hive_partitioning=true, union_by_name=true)
    WHERE names.primary IS NOT NULL
      AND (bbox.xmin + bbox.xmax) / 2.0 BETWEEN ? AND ?
      AND (bbox.ymin + bbox.ymax) / 2.0 BETWEEN ? AND ?
"""


class OvertureAdapter(SourceAdapter):
    """Reads the Overture places theme via DuckDB. See module docstring."""

    name = "overture"

    def __init__(self, *, data_path: str, load_remote_extensions: bool | None = None):
        # `data_path` is a parquet path/glob: a local extract, or the public
        # release on S3 (e.g. "s3://overturemaps-us-west-2/release/<rel>/
        # theme=places/type=place/*"). Remote paths need DuckDB's httpfs extension.
        self.data_path = data_path
        if load_remote_extensions is None:
            load_remote_extensions = data_path.startswith(("s3://", "http://", "https://"))
        self.load_remote_extensions = load_remote_extensions

    def _connect(self):
        import duckdb  # lazy: only needed for a real fetch, not for tests/import

        conn = duckdb.connect(database=":memory:")
        if self.load_remote_extensions:
            conn.execute("INSTALL httpfs; LOAD httpfs;")
            # Overture's bucket is public; anonymous, region us-west-2.
            conn.execute("SET s3_region='us-west-2';")
        return conn

    def _query_rows(self, bbox: tuple[float, float, float, float]) -> Iterator[dict]:
        """Yield raw column dicts for places inside ``bbox``. Patched in tests."""
        min_lon, min_lat, max_lon, max_lat = bbox
        conn = self._connect()
        try:
            cursor = conn.execute(_SELECT, [self.data_path, min_lon, max_lon, min_lat, max_lat])
            columns = [c[0] for c in cursor.description]
            while True:
                batch = cursor.fetchmany(1000)
                if not batch:
                    break
                for row in batch:
                    yield dict(zip(columns, row, strict=True))
        finally:
            conn.close()

    @staticmethod
    def _first_address(addresses) -> dict:
        if not addresses:
            return {}
        first = addresses[0] if isinstance(addresses, (list, tuple)) else addresses
        if not isinstance(first, dict):
            return {}
        return first

    @classmethod
    def row_to_raw_place(cls, row: dict) -> RawPlace | None:
        lon, lat = row.get("lon"), row.get("lat")
        if lon is None or lat is None:
            return None
        name = (row.get("name") or "").strip()
        if not name:
            return None
        category = row.get("category")
        alternate = list(row.get("alternate") or [])
        addr = cls._first_address(row.get("addresses"))
        websites = list(row.get("websites") or [])
        tags = {
            "overture:category": category,
            "overture:alternate": alternate,
        }
        if websites:
            tags["overture:website"] = websites[0]
        return RawPlace(
            source="overture",
            external_id=str(row["id"]),
            name=name,
            lon=float(lon),
            lat=float(lat),
            tags=tags,
            website=websites[0] if websites else "",
            address={
                "street": addr.get("street", "") or addr.get("freeform", ""),
                "housenumber": addr.get("housenumber", ""),
                "city": addr.get("locality", ""),
                "postcode": addr.get("postcode", ""),
                "country": addr.get("country", ""),
            },
        )

    def fetch(self, *, city=None, bbox=None, limit=None) -> Iterator[RawPlace]:
        if not bbox:
            # Overture has no admin-area index like Overpass; we filter by bbox.
            raise ValueError("OvertureAdapter requires a bbox (minlon,minlat,maxlon,maxlat)")
        logger.info("Querying Overture parquet at %s", self.data_path)
        count = 0
        for row in self._query_rows(bbox):
            raw = self.row_to_raw_place(row)
            if raw is None:
                continue
            yield raw
            count += 1
            if limit and count >= limit:
                break
