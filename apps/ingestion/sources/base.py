from abc import ABC, abstractmethod
from collections.abc import Iterator
from dataclasses import dataclass, field


@dataclass
class RawPlace:
    """A normalized place from any source, ready to upsert into Place."""

    source: str
    name: str
    lon: float
    lat: float
    tags: dict
    address: dict = field(default_factory=dict)
    opening_hours_raw: str = ""
    osm_type: str | None = None
    osm_id: int | None = None
    external_id: str | None = None


class SourceAdapter(ABC):
    """A place-data source. Adapters normalize their source into RawPlace so
    the ingestion command stays source-agnostic."""

    name: str

    @abstractmethod
    def fetch(
        self,
        *,
        city: str | None = None,
        bbox: tuple[float, float, float, float] | None = None,
        limit: int | None = None,
    ) -> Iterator[RawPlace]:
        raise NotImplementedError
