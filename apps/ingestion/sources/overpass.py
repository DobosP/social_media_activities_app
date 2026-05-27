import logging
import time
from collections.abc import Iterator

import requests

from .base import RawPlace, SourceAdapter

logger = logging.getLogger(__name__)

# Tag selectors fetched in one combined Overpass query. `nwr` = node+way+relation.
SELECTORS = [
    '["leisure"="pitch"]["sport"]',
    '["leisure"="sports_centre"]',
    '["leisure"="playground"]',
    '["leisure"="amusement_arcade"]',
    # Parks & green public spaces where activities happen outdoors.
    '["leisure"="park"]',
    '["leisure"="garden"]',
    '["leisure"="nature_reserve"]',
    '["leisure"="dog_park"]',
    # Reservation-friendly venues (often have a website/phone).
    '["leisure"="fitness_centre"]',
    '["leisure"="swimming_pool"]',
    '["leisure"="sports_hall"]',
    '["leisure"="stadium"]',
    # Public/cultural places known for activities.
    '["amenity"="library"]',
    '["amenity"="archive"]',
    '["amenity"="community_centre"]',
    '["amenity"="arts_centre"]',
    '["amenity"="theatre"]',
    '["amenity"="public_bookcase"]',
    '["amenity"="table_tennis_table"]',
    '["amenity"="internet_cafe"]',
    '["amenity"="cafe"]["board_games"]',
    '["shop"="games"]',
    '["shop"="boardgames"]',
    '["shop"="books"]',
    '["shop"="video_games"]',
]

RETRYABLE_STATUS = {429, 502, 503, 504}


class OverpassAdapter(SourceAdapter):
    name = "osm"

    def __init__(
        self,
        *,
        endpoint: str,
        user_agent: str,
        timeout: int = 190,
        max_retries: int = 3,
    ):
        self.endpoint = endpoint
        self.user_agent = user_agent
        self.timeout = timeout
        self.max_retries = max_retries

    def _build_query(self, *, city: str | None, bbox) -> str:
        if bbox:
            min_lon, min_lat, max_lon, max_lat = bbox
            # Overpass bounding box order is (south,west,north,east).
            region = f"({min_lat},{min_lon},{max_lat},{max_lon})"
            body = "\n".join(f"  nwr{sel}{region};" for sel in SELECTORS)
            return f"[out:json][timeout:180];\n(\n{body}\n);\nout center tags;"
        area = f'area["name"="{city}"]["boundary"="administrative"]->.a;'
        body = "\n".join(f"  nwr{sel}(area.a);" for sel in SELECTORS)
        return f"[out:json][timeout:180];\n{area}\n(\n{body}\n);\nout center tags;"

    def _post(self, query: str) -> dict:
        delay = 5
        last_exc: Exception | None = None
        for attempt in range(1, self.max_retries + 1):
            try:
                resp = requests.post(
                    self.endpoint,
                    data={"data": query},
                    headers={"User-Agent": self.user_agent},
                    timeout=self.timeout,
                )
                if resp.status_code in RETRYABLE_STATUS:
                    logger.warning(
                        "Overpass HTTP %s (attempt %s/%s); backing off %ss",
                        resp.status_code,
                        attempt,
                        self.max_retries,
                        delay,
                    )
                    time.sleep(delay)
                    delay *= 3
                    continue
                resp.raise_for_status()
                return resp.json()
            except requests.RequestException as exc:
                last_exc = exc
                logger.warning(
                    "Overpass request failed (attempt %s/%s): %s",
                    attempt,
                    self.max_retries,
                    exc,
                )
                time.sleep(delay)
                delay *= 3
        raise RuntimeError(f"Overpass request failed after {self.max_retries} attempts: {last_exc}")

    @staticmethod
    def element_to_raw_place(element: dict) -> RawPlace | None:
        element_type = element.get("type")
        if element_type == "node":
            lat, lon = element.get("lat"), element.get("lon")
        else:  # way / relation -> use the centroid from `out center`
            center = element.get("center") or {}
            lat, lon = center.get("lat"), center.get("lon")
        if lat is None or lon is None:
            return None
        tags = element.get("tags") or {}
        address = {
            "street": tags.get("addr:street", ""),
            "housenumber": tags.get("addr:housenumber", ""),
            "city": tags.get("addr:city", ""),
            "postcode": tags.get("addr:postcode", ""),
            "country": tags.get("addr:country", ""),
        }
        website = (
            tags.get("website")
            or tags.get("contact:website")
            or tags.get("url")
            or tags.get("contact:url")
            or ""
        )
        phone = tags.get("phone") or tags.get("contact:phone") or ""
        return RawPlace(
            source="osm",
            osm_type=element_type,
            osm_id=element.get("id"),
            name=tags.get("name", ""),
            lon=float(lon),
            lat=float(lat),
            tags=tags,
            address=address,
            opening_hours_raw=tags.get("opening_hours", ""),
            website=website,
            phone=phone,
        )

    def fetch(self, *, city=None, bbox=None, limit=None) -> Iterator[RawPlace]:
        if not city and not bbox:
            raise ValueError("Provide either city or bbox")
        query = self._build_query(city=city, bbox=bbox)
        logger.info("Querying Overpass (%s)", "bbox" if bbox else f"city={city}")
        data = self._post(query)
        count = 0
        for element in data.get("elements", []):
            raw = self.element_to_raw_place(element)
            if raw is None:
                continue
            yield raw
            count += 1
            if limit and count >= limit:
                break
