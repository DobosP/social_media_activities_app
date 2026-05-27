from collections.abc import Iterator

from .base import RawPlace, SourceAdapter


class OvertureAdapter(SourceAdapter):
    """FUTURE source: Overture Maps places theme (free, open data).

    Implementation sketch: use DuckDB to ``read_parquet`` the Overture places
    release from its public S3 bucket, filter rows to the target bbox, map each
    row's ``categories.primary`` to our activity slugs (a mapping table
    analogous to the OSM one), and yield ``RawPlace(source="overture", ...)``.
    Kept as a stub so the source-adapter seam is proven without the dependency.
    """

    name = "overture"

    def fetch(self, *, city=None, bbox=None, limit=None) -> Iterator[RawPlace]:
        raise NotImplementedError(
            "Overture adapter is a future source; see the class docstring for the plan."
        )
