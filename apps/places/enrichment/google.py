"""Optional Google Places enrichment — disabled by default.

Policy (see docs/DATA_AND_INTEGRATIONS.md): Google is **enrichment only**, never a
place source. We don't re-import places from Google; we enrich existing ones with
**live status** (open-now), the official maps link, and durable hours where the
spend is justified. It's a paid API, so the whole path is gated behind a setting
flag + API key and is a no-op unless explicitly enabled.

Network calls use ``requests`` and are made lazily; tests patch :meth:`_get` so no
network or key is required. Live "open now" is transient and returned to the
caller — not persisted; only durable signals (maps link, regular hours) are stored,
in the existing ``raw_tags`` / ``opening_hours`` fields (no schema change).
"""

from __future__ import annotations

import logging

from django.conf import settings

logger = logging.getLogger(__name__)

# Places API (New) — Place Details. We request only the fields we use.
_DETAILS_URL = "https://places.googleapis.com/v1/places/{place_id}"
_SEARCH_URL = "https://places.googleapis.com/v1/places:searchText"


class GooglePlacesError(RuntimeError):
    pass


class GooglePlacesEnricher:
    def __init__(self, *, api_key: str | None = None, enabled: bool | None = None):
        self.api_key = (
            api_key if api_key is not None else getattr(settings, "GOOGLE_PLACES_API_KEY", "")
        )
        if enabled is None:
            enabled = getattr(settings, "GOOGLE_PLACES_ENABLED", False)
        self.enabled = bool(enabled and self.api_key)

    def _get(self, url: str, *, params: dict, field_mask: str) -> dict:
        import requests

        resp = requests.get(
            url,
            params=params,
            headers={
                "X-Goog-Api-Key": self.api_key,
                "X-Goog-FieldMask": field_mask,
            },
            timeout=15,
        )
        resp.raise_for_status()
        return resp.json()

    def _post(self, url: str, *, json: dict, field_mask: str) -> dict:
        import requests

        resp = requests.post(
            url,
            json=json,
            headers={
                "X-Goog-Api-Key": self.api_key,
                "X-Goog-FieldMask": field_mask,
            },
            timeout=15,
        )
        resp.raise_for_status()
        return resp.json()

    def find_place_id(self, place) -> str | None:
        """Resolve a Google place id for one of our places via text search
        (name + city), biased to its location. Returns ``None`` if not found."""
        if not self.enabled:
            return None
        query = " ".join(p for p in (place.name, place.address_city) if p).strip()
        if not query:
            return None
        body = {
            "textQuery": query,
            "maxResultCount": 1,
            "locationBias": {
                "circle": {
                    "center": {"latitude": place.location.y, "longitude": place.location.x},
                    "radius": 200.0,
                }
            },
        }
        data = self._post(_SEARCH_URL, json=body, field_mask="places.id")
        places = data.get("places") or []
        return places[0]["id"] if places else None

    def live_status(self, place_id: str) -> dict:
        """Fetch enrichment for a Google place id. Returns a dict with (when
        available): ``open_now`` (transient), ``maps_uri``, ``regular_hours``
        (raw weekday descriptions). Raises if the enricher is disabled."""
        if not self.enabled:
            raise GooglePlacesError("Google Places enrichment is disabled")
        data = self._get(
            _DETAILS_URL.format(place_id=place_id),
            params={},
            field_mask=(
                "id,googleMapsUri,currentOpeningHours,regularOpeningHours,"
                "websiteUri,internationalPhoneNumber,rating,userRatingCount,primaryType"
            ),
        )
        current = data.get("currentOpeningHours") or {}
        regular = data.get("regularOpeningHours") or {}
        return {
            "place_id": data.get("id", place_id),
            "open_now": current.get("openNow"),
            "maps_uri": data.get("googleMapsUri"),
            "regular_hours": regular.get("weekdayDescriptions") or [],
            "website": data.get("websiteUri") or "",
            "phone": data.get("internationalPhoneNumber") or "",
            "rating": data.get("rating"),
            "rating_count": data.get("userRatingCount"),
            "primary_type": data.get("primaryType") or "",
        }

    def enrich_place(self, place) -> dict | None:
        """End-to-end: resolve the id, fetch status, persist durable signals
        (maps link in ``raw_tags['google']``), and return the live status dict.
        Returns ``None`` if disabled or unresolved."""
        if not self.enabled:
            return None
        place_id = self.find_place_id(place)
        if not place_id:
            return None
        status = self.live_status(place_id)
        google_meta = {"place_id": status["place_id"]}
        for key in ("maps_uri", "rating", "rating_count", "primary_type"):
            if status.get(key):
                google_meta[key] = status[key]
        place.raw_tags = {**(place.raw_tags or {}), "google": google_meta}
        update_fields = ["raw_tags"]
        # Backfill durable contact details Google can supply when we're missing them.
        if not place.website and status.get("website"):
            place.website = status["website"]
            update_fields.append("website")
        if not place.phone and status.get("phone"):
            place.phone = status["phone"]
            update_fields.append("phone")
        place.save(update_fields=update_fields)
        return status
