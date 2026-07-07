"""Wikimedia Commons cover resolution — free licenses only, cached with attribution.

The ladder (ADR-0019 §2): a place's OSM ``raw_tags`` may carry ``wikimedia_commons``
("File:…"), ``image`` (only honoured when it is a commons.wikimedia.org /
upload.wikimedia.org URL), or ``wikidata`` (QID → image property P18). Whichever file
title resolves first is fetched THROUGH the Commons API (thumb URL + author + license
from ``extmetadata``) and the thumbnail bytes are cached in our object storage — never
hot-linked, so visitors' IPs stay with us and pages keep working if Commons is down.

Commons only hosts free licenses / public domain, so anything it serves is storable;
the license short name + author are persisted on the cover and MUST be rendered
wherever the image appears. Network goes through :func:`_api_get` /
:func:`_download`, which tests patch — no network required.
"""

import logging
import uuid

from django.conf import settings

logger = logging.getLogger(__name__)

_COMMONS_API_DEFAULT = "https://commons.wikimedia.org/w/api.php"
_THUMB_WIDTH = 800
_MAX_IMAGE_BYTES = 8 * 1024 * 1024
_EXT_BY_MIME = {
    "image/jpeg": "jpg",
    "image/png": "png",
    "image/webp": "webp",
}
# raw_tags marker so re-runs skip places already checked (cleared by re-ingest only if
# the ingest adapter rewrites raw_tags wholesale, which re-opens the question — fine).
CHECKED_MARKER = "cover_checked"


def commons_file_title(place) -> str | None:
    """The Commons "File:…" title referenced by the place's OSM tags, if any."""
    tags = place.raw_tags or {}
    value = tags.get("wikimedia_commons")
    if isinstance(value, str) and value.startswith("File:"):
        return value
    image = tags.get("image")
    if isinstance(image, str):
        for host in ("commons.wikimedia.org/wiki/File:", "upload.wikimedia.org/"):
            if host in image and "File:" in image:
                return "File:" + image.split("File:", 1)[1]
    return None


class CommonsCoverResolver:
    def __init__(self, *, api_url: str | None = None, user_agent: str | None = None):
        self.api_url = api_url or getattr(settings, "COMMONS_API_URL", _COMMONS_API_DEFAULT)
        self.user_agent = user_agent or getattr(
            settings, "INGEST_USER_AGENT", "social-activities-app"
        )

    # --- network seams (patched in tests) -------------------------------------------

    def _api_get(self, params: dict) -> dict:
        from apps.safety.net import safe_get

        resp = safe_get(
            self.api_url,
            params={"format": "json", **params},
            headers={"User-Agent": self.user_agent},
            timeout=30,
            max_bytes=2 * 1024 * 1024,
        )
        resp.raise_for_status()
        return resp.json()

    def _download(self, url: str) -> bytes:
        from apps.safety.net import safe_get

        resp = safe_get(
            url,
            headers={"User-Agent": self.user_agent},
            timeout=60,
            max_bytes=_MAX_IMAGE_BYTES,
        )
        resp.raise_for_status()
        return resp.content

    # --- resolution ------------------------------------------------------------------

    def image_title_via_wikidata(self, qid: str) -> str | None:
        """Commons file title from a Wikidata QID's image property (P18), via the
        wbgetclaims API (lighter than SPARQL for single-property lookups)."""
        try:
            data = self._api_get(
                {
                    "action": "wbgetclaims",
                    "entity": qid,
                    "property": "P18",
                }
            )
        except Exception as exc:  # external endpoint: log and skip
            logger.warning("Commons P18 lookup failed for %s: %s", qid, exc)
            return None
        claims = data.get("claims", {}).get("P18", [])
        for claim in claims:
            value = claim.get("mainsnak", {}).get("datavalue", {}).get("value")
            if isinstance(value, str) and value:
                return f"File:{value}"
        return None

    def imageinfo(self, file_title: str) -> dict | None:
        """Thumb URL + attribution metadata for a Commons file, or None."""
        try:
            data = self._api_get(
                {
                    "action": "query",
                    "titles": file_title,
                    "prop": "imageinfo",
                    "iiprop": "url|mime|size|extmetadata",
                    "iiurlwidth": _THUMB_WIDTH,
                }
            )
        except Exception as exc:
            logger.warning("Commons imageinfo failed for %s: %s", file_title, exc)
            return None
        pages = data.get("query", {}).get("pages", {})
        for page in pages.values():
            infos = page.get("imageinfo") or []
            if infos:
                return infos[0]
        return None

    @staticmethod
    def _meta(info: dict, key: str) -> str:
        value = (info.get("extmetadata") or {}).get(key, {}).get("value", "")
        if not isinstance(value, str):
            return ""
        # extmetadata values may carry HTML — keep plain text only.
        import re

        return re.sub(r"<[^>]+>", "", value).strip()

    def resolve(self, place):
        """Fetch + store a Commons cover for ``place``. Returns the PlaceCover or None.

        Idempotent per place: existing covers are never replaced here (business
        uploads outrank wikimedia; re-resolution is a manual decision).
        """
        from apps.media.storage import get_storage
        from apps.places.models import PlaceCover

        if getattr(place, "cover", None) and place.cover.storage_key:
            return place.cover

        title = commons_file_title(place)
        if title is None:
            qid = (place.raw_tags or {}).get("wikidata")
            if isinstance(qid, str) and qid.startswith("Q") and qid[1:].isdigit():
                title = self.image_title_via_wikidata(qid)
        if title is None:
            return None

        info = self.imageinfo(title)
        if not info:
            return None
        thumb_url = info.get("thumburl") or info.get("url") or ""
        mime = info.get("thumbmime") or info.get("mime") or ""
        ext = _EXT_BY_MIME.get(mime)
        if not thumb_url or ext is None:
            logger.info("Commons file %s skipped (mime=%r)", title, mime)
            return None

        try:
            data = self._download(thumb_url)
        except Exception as exc:
            logger.warning("Commons thumb download failed for %s: %s", title, exc)
            return None

        artist = self._meta(info, "Artist")
        license_short = self._meta(info, "LicenseShortName")
        attribution = ", ".join(p for p in (artist, license_short) if p)
        if attribution:
            attribution = f"{attribution}, via Wikimedia Commons"[:255]
        else:
            attribution = "Wikimedia Commons"

        storage_key = f"place-covers/{uuid.uuid4().hex}.{ext}"
        get_storage().save(storage_key, data, content_type=mime)
        cover, _created = PlaceCover.objects.update_or_create(
            place=place,
            defaults={
                "source": PlaceCover.Source.WIKIMEDIA,
                "storage_key": storage_key,
                "content_type": mime,
                "byte_size": len(data),
                "width": int(info.get("thumbwidth") or 0),
                "height": int(info.get("thumbheight") or 0),
                "attribution": attribution,
                "license_name": license_short[:120],
                "source_page_url": (info.get("descriptionurl") or "")[:500],
                "alt_text": (place.display_name or "")[:140],
            },
        )
        return cover
