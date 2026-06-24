"""RO-EDU scraper adapter — ingest venues from the romania_scraper data platform.

Reads the ``venues`` data product (cultural institutions with lat/lon/address/city
extracted by ``romania_scraper events build``) over the read-only HTTP API and
normalizes each into a ``RawPlace`` for the source-agnostic ``ingest_places``
command. Register it:

    # settings.INGESTION_EXTRA_ADAPTERS = {
    #     "roedu": "apps.ingestion.sources.ro_scraper.RomaniaScraperAdapter"}
    python manage.py ingest_places --source=roedu --city="Cluj-Napoca"

Notes / invariants:
- ``source="roedu"`` is deliberately NOT one of the OSM/Overture child-venue
  classes, so a scraped venue stays child-venue-UNKNOWN (fail-closed) until a
  curated allowlist promotes it — never route it through the OSM tag branch
  (design §11 M4).
- Venues carry only factual fields (name/address/lat/lon) — no copyrighted prose.
- We synthesize OSM-style ``tags`` from the venue name so ``ingestion.mapping``
  can attach a ``PlaceActivity`` edge; otherwise the venue is invisible under
  ``?activity=`` filters (design §11 nit). Refine in ``apps/ingestion/mapping.py``.
"""

from __future__ import annotations

import os
from collections.abc import Iterator

from .base import RawPlace, SourceAdapter
from .roedu_client import RoeduClient

# Heuristic: Romanian venue-name keyword -> OSM-style tag the mapping rules read.
_NAME_TAGS: list[tuple[tuple[str, ...], dict]] = [
    (("opera", "operă"), {"amenity": "theatre", "theatre:genre": "opera"}),
    (("filarmonic", "filarmonică", "concert"), {"amenity": "theatre", "theatre:type": "concert"}),
    (("teatru", "theater", "theatre"), {"amenity": "theatre"}),
    (("muzeu", "museum", "muzeul"), {"tourism": "museum"}),
    (("galeri", "gallery", "artă", "arta"), {"tourism": "gallery"}),
    (("bibliotec", "library"), {"amenity": "library"}),
    (("cinema", "film"), {"amenity": "cinema"}),
]
_DEFAULT_TAGS = {"amenity": "arts_centre"}
_ATTRIBUTION_KEYS = ("attribution", "credit", "source_name", "publisher", "provider")
_LICENSE_KEYS = ("license_name", "license", "licence", "license_title")
_PROVENANCE_KEYS = ("provenance_url", "source_url", "url")


def _tags_for(name: str) -> dict:
    low = (name or "").lower()
    for needles, tags in _NAME_TAGS:
        if any(n in low for n in needles):
            return dict(tags)
    return dict(_DEFAULT_TAGS)


def _first_text(record: dict, keys: tuple[str, ...], *, max_length: int) -> str:
    for key in keys:
        value = record.get(key)
        if value is None:
            continue
        text = str(value).strip()
        if text:
            return text[:max_length]
    return ""


class RomaniaScraperAdapter(SourceAdapter):
    name = "roedu"

    def __init__(self, client: RoeduClient | None = None) -> None:
        self._client = client or RoeduClient(
            base_url=os.environ.get("ROEDU_API_URL"),
            api_key=os.environ.get("ROEDU_API_KEY", "social-app-dev"),
        )

    def fetch(
        self,
        *,
        city: str | None = None,
        bbox: tuple[float, float, float, float] | None = None,
        limit: int | None = None,
    ) -> Iterator[RawPlace]:
        filters: dict = {}
        if city:
            filters["city"] = city
        for v in self._client.iter("venues", max_records=limit, **filters):
            lat, lon = v.get("lat"), v.get("lon")
            if lat is None or lon is None:
                continue  # a Place needs a point to be proximity-discoverable
            yield RawPlace(
                source="roedu",
                name=v.get("name") or "",
                lon=float(lon),
                lat=float(lat),
                tags=_tags_for(v.get("name") or ""),
                address={
                    "street": v.get("address") or "",
                    "city": v.get("city") or city or "",
                    "country": "RO",
                },
                website=v.get("source_url") or "",
                external_id=str(v.get("id") or ""),
                attribution=_first_text(v, _ATTRIBUTION_KEYS, max_length=255),
                license_name=_first_text(v, _LICENSE_KEYS, max_length=120),
                provenance_url=_first_text(v, _PROVENANCE_KEYS, max_length=500),
            )
