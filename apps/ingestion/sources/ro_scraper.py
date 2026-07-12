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
from .roedu_client import (
    SOCIAL_APP_PACK_ID,
    RoeduClient,
    is_canonical_social_app_pack_item,
    require_canonical_social_pack,
)

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
_APP_PACK_PLACE_KINDS = frozenset({"venue", "place"})


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


def _app_pack_tags_for(item: dict) -> dict:
    facets = item.get("facets") if isinstance(item.get("facets"), dict) else {}
    stable_tags = item.get("tags") if isinstance(item.get("tags"), list) else []
    category = str(
        facets.get("venue_category") or facets.get("place_category") or facets.get("category") or ""
    ).strip()

    tags = _tags_for(" ".join([item.get("title") or "", category]))
    if stable_tags:
        tags["roedu:tags"] = [str(tag) for tag in stable_tags if str(tag).strip()]
    for facet in ("city", "county", "category", "venue_category", "place_category"):
        value = facets.get(facet)
        if value is not None and str(value).strip():
            tags[f"roedu:{facet}"] = str(value).strip()
    if item.get("source"):
        tags["roedu:source"] = str(item["source"]).strip()
    if item.get("confidence") is not None:
        tags["roedu:confidence"] = item["confidence"]
    return tags


def app_pack_item_to_raw_place(item: dict, *, city: str | None = None) -> RawPlace | None:
    if item.get("kind") not in _APP_PACK_PLACE_KINDS or not is_canonical_social_app_pack_item(item):
        return None
    location = item.get("location") if isinstance(item.get("location"), dict) else {}
    lat, lon = location.get("lat"), location.get("lon")
    if lat is None or lon is None:
        return None
    facets = item.get("facets") if isinstance(item.get("facets"), dict) else {}
    address = item.get("address") if isinstance(item.get("address"), dict) else {}
    return RawPlace(
        source="roedu",
        name=item.get("title") or "",
        lon=float(lon),
        lat=float(lat),
        tags=_app_pack_tags_for(item),
        address={
            "street": address.get("street") or "",
            "city": address.get("city") or facets.get("city") or city or "",
            "county": address.get("county") or facets.get("county") or "",
            "country": address.get("country") or "RO",
        },
        website=item.get("website") or "",
        external_id=str(item.get("id") or ""),
        attribution=str(item.get("source") or "")[:255],
        license_name=str(item.get("license") or "")[:120],
        provenance_url="",
    )


class RomaniaScraperAdapter(SourceAdapter):
    name = "roedu"
    canonical_app_pack = SOCIAL_APP_PACK_ID

    def __init__(self, client: RoeduClient | None = None, *, app_pack: str | None = None) -> None:
        self._client = client or RoeduClient(
            base_url=os.environ.get("ROEDU_API_URL"),
            api_key=os.environ.get("ROEDU_API_KEY", "social-app-dev"),
        )
        configured_pack = app_pack or os.environ.get("ROEDU_APP_PACK") or None
        self.app_pack = require_canonical_social_pack(configured_pack) if configured_pack else None

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
        if self.app_pack:
            filters.setdefault("kind", "venue")
            for item in self._client.iter_app_pack(self.app_pack, max_records=limit, **filters):
                if not is_canonical_social_app_pack_item(item):
                    continue
                raw = app_pack_item_to_raw_place(item, city=city)
                if raw is not None:
                    yield raw
            return
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
