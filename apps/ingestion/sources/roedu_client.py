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
from dataclasses import dataclass

REDISTRIBUTABLE_ACCESS_TYPES = frozenset({"public_document", "open_license", "public_domain"})


class RoeduContractError(ValueError):
    """The serving response changed identity while it was being paged."""


@dataclass(frozen=True)
class AppPackRead:
    items: tuple[dict, ...]
    pack_id: str
    snapshot_id: str
    release_id: str
    snapshot_generated_at: str
    snapshot_mode: str
    snapshot_complete: bool


def _consistent_metadata(current: str, incoming, field: str) -> str:
    incoming = str(incoming or "").strip()
    if current and incoming and current != incoming:
        raise RoeduContractError(f"app-pack {field} changed while paging")
    return current or incoming


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

    def read_app_pack(
        self,
        pack: str,
        *,
        app: str = "social_media_activities_app",
        layer: str = "redistributable",
        limit: int = 200,
        max_records: int | None = None,
        **filters,
    ) -> AppPackRead:
        """Read one immutable app-pack view and retain its snapshot identity.

        The existing iterator remains the lightweight adapter boundary. Sync jobs
        use this materialized form because absence reconciliation is safe only
        after every page has carried one consistent, complete snapshot identity.
        """
        if layer != "redistributable":
            return AppPackRead((), "", "", "", "", "", False)

        cursor = None
        seen_cursors: set[str] = set()
        items: list[dict] = []
        pack_id = snapshot_id = release_id = generated_at = snapshot_mode = ""
        declared_complete = True
        identity_complete = True
        items_valid = True
        locally_complete = False
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
                break
            page_generated_at = page.get("snapshot_generated_at") or page.get("generated_at")
            identity_complete = identity_complete and all(
                bool(str(value or "").strip())
                for value in (
                    page.get("pack_id"),
                    page.get("snapshot_id"),
                    page.get("release_id"),
                    page_generated_at,
                    page.get("snapshot_mode"),
                )
            )
            pack_id = _consistent_metadata(pack_id, page.get("pack_id"), "pack_id")
            snapshot_id = _consistent_metadata(snapshot_id, page.get("snapshot_id"), "snapshot_id")
            release_id = _consistent_metadata(release_id, page.get("release_id"), "release_id")
            generated_at = _consistent_metadata(
                generated_at,
                page_generated_at,
                "snapshot_generated_at",
            )
            snapshot_mode = _consistent_metadata(
                snapshot_mode, page.get("snapshot_mode"), "snapshot_mode"
            )
            declared_complete = declared_complete and page.get("snapshot_complete") is True
            page_items = page.get("items", [])
            if not isinstance(page_items, list):
                items_valid = False
                page_items = []
            for item in page_items:
                if not isinstance(item, dict) or not is_redistributable_app_pack_item(item):
                    items_valid = False
                    continue
                items.append(item)
                if max_records and len(items) >= max_records:
                    return AppPackRead(
                        tuple(items),
                        pack_id,
                        snapshot_id,
                        release_id,
                        generated_at,
                        snapshot_mode,
                        False,
                    )
            pagination = page.get("pagination") or {}
            next_cursor = pagination.get("next_cursor")
            if not next_cursor:
                locally_complete = True
                break
            next_cursor = str(next_cursor)
            if next_cursor in seen_cursors:
                raise RoeduContractError("app-pack cursor repeated while paging")
            seen_cursors.add(next_cursor)
            cursor = next_cursor

        strong_metadata = bool(
            identity_complete
            and items_valid
            and pack_id
            and snapshot_id
            and release_id
            and generated_at
            and snapshot_mode == "full"
        )
        return AppPackRead(
            tuple(items),
            pack_id,
            snapshot_id,
            release_id,
            generated_at,
            snapshot_mode,
            locally_complete and declared_complete and strong_metadata,
        )
