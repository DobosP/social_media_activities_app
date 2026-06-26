"""roedu_client — tiny, dependency-free client for the RO-EDU data platform.

Stdlib only (urllib), so it vendors cleanly into any consuming app (Django, etc.)
without adding a dependency. Talks to romania_scraper.dataapi over HTTP, handles
cursor pagination, and re-exposes the platform's license gate as the server
already enforces it (the client trusts but the server is fail-closed).

    from roedu_client import RoeduClient
    c = RoeduClient("http://localhost:8077", api_key="social-app-dev")
    for venue in c.iter("venues", city="Cluj-Napoca"):
        ...
    for chunk in c.iter("education_chunks", document_type="curriculum", language="ro"):
        ...

Config via env when vendored into an app:
    ROEDU_API_URL   (default http://localhost:8077)
    ROEDU_API_KEY
"""

from __future__ import annotations

import json
import os
import urllib.parse
import urllib.request
from collections.abc import Iterator

REDISTRIBUTABLE_ACCESS_TYPES = frozenset({"public_document", "open_license", "public_domain"})


def is_redistributable_app_pack_item(item: dict) -> bool:
    """Return whether an app-pack item is safe for public/app consumers.

    The serving layer is expected to enforce this first. This client repeats the
    gate so missing/unknown legal or GDPR metadata fails closed if a fixture or
    future endpoint regresses.
    """
    return (
        item.get("redistributable") is True
        and item.get("access_type") in REDISTRIBUTABLE_ACCESS_TYPES
        and bool(str(item.get("legal_basis") or "").strip())
        and item.get("gdpr_relevant") is False
    )


class RoeduClient:
    def __init__(
        self,
        base_url: str | None = None,
        api_key: str | None = None,
        *,
        timeout: float = 30.0,
    ) -> None:
        default_url = os.environ.get("ROEDU_API_URL", "http://localhost:8077")
        self.base_url = (base_url or default_url).rstrip("/")
        self.api_key = api_key or os.environ.get("ROEDU_API_KEY", "")
        self.timeout = timeout

    def _get(self, path: str, params: dict | None = None) -> dict:
        url = f"{self.base_url}{path}"
        if params:
            clean = {k: v for k, v in params.items() if v is not None}
            url += "?" + urllib.parse.urlencode(clean)
        headers = {"X-API-Key": self.api_key, "Accept": "application/json"}
        req = urllib.request.Request(url, headers=headers)
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:  # noqa: S310
            return json.loads(resp.read().decode("utf-8"))

    def health(self) -> dict:
        return self._get("/v1/health")

    def products(self) -> list[dict]:
        return self._get("/v1/products")

    def page(self, product: str, *, cursor: str | None = None, limit: int = 200, **filters) -> dict:
        params = {"cursor": cursor, "limit": limit, **filters}
        return self._get(f"/v1/products/{product}", params)

    def app_pack_page(
        self,
        pack: str,
        *,
        app: str = "social_media_activities_app",
        layer: str = "redistributable",
        cursor: str | None = None,
        limit: int = 200,
        **filters,
    ) -> dict:
        params = {"layer": layer, "cursor": cursor, "limit": limit, **filters}
        return self._get(f"/v1/app-packs/{app}/{pack}", params)

    def iter(
        self, product: str, *, limit: int = 200, max_records: int | None = None, **filters
    ) -> Iterator[dict]:
        """Yield every record of a product, following cursors. Stops at max_records."""
        cursor = None
        seen = 0
        while True:
            page = self.page(product, cursor=cursor, limit=limit, **filters)
            if not page.get("available", False):
                return
            for rec in page.get("records", []):
                yield rec
                seen += 1
                if max_records and seen >= max_records:
                    return
            cursor = page.get("next_cursor")
            if not cursor:
                return

    def iter_app_pack(
        self,
        pack: str,
        *,
        app: str = "social_media_activities_app",
        layer: str = "redistributable",
        limit: int = 200,
        max_records: int | None = None,
        **filters,
    ) -> Iterator[dict]:
        """Yield redistributable app-pack items, following app-pack cursors.

        Public social consumers only read the redistributable layer. Requests for
        other layers deliberately yield nothing because this app has no proven
        internal/all HTTP gate.
        """
        if layer != "redistributable":
            return

        cursor = None
        seen = 0
        while True:
            page = self.app_pack_page(
                pack,
                app=app,
                layer=layer,
                cursor=cursor,
                limit=limit,
                **filters,
            )
            if page.get("layer") != "redistributable":
                return
            for item in page.get("items", []):
                if not is_redistributable_app_pack_item(item):
                    continue
                yield item
                seen += 1
                if max_records and seen >= max_records:
                    return
            pagination = page.get("pagination") or {}
            cursor = pagination.get("next_cursor")
            if not cursor:
                return
