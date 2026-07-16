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

import ipaddress
import json
import math
import os
import re
import urllib.parse
import urllib.request
from collections.abc import Iterator
from dataclasses import dataclass
from datetime import datetime
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

REDISTRIBUTABLE_ACCESS_TYPES = frozenset({"public_document", "open_license", "public_domain"})
SOCIAL_APP_NAME = "social_media_activities_app"
SOCIAL_APP_PACK_ID = "roedu:social_media_activities_app:events_places:v1"
SOCIAL_APP_PACK_SCHEMA_VERSION = 1
POLICY_SCHEMA_VERSION = 4
POLICY_RULESET_VERSION = 6
POLICY_RULESET_HASH = "07f27d3c9a5e5898ba7cfac686c645713114dd9c13d72ecc054570d368daf58d"

_SHA256 = re.compile(r"^[0-9a-f]{64}$")
_PROMOTED_RELEASE_ID = re.compile(r"^sha256-[0-9a-f]{64}$")
_CATEGORY = re.compile(r"^[a-z][a-z0-9_]{0,63}$")
_ACQUISITION_LANES = frozenset({"web_http", "sanctioned_api", "bulk_download", "derived_member"})
_EVENT_STATUSES = frozenset(
    {
        "scheduled",
        "rescheduled",
        "postponed",
        "cancelled",
        "sold_out",
        "moved_online",
        "expired",
    }
)
_AVAILABILITY = frozenset({"available", "limited", "sold_out", "unknown"})
_ATTESTATION_FIELDS = frozenset(
    {
        "decision_id",
        "evidence_digest",
        "clearance_digest",
        "subject_sha256",
        "schema_version",
        "ruleset_version",
        "ruleset_hash",
        "action",
        "effect",
        "reasons",
        "obligations",
        "capture_id",
        "capture_schema_version",
        "acquisition_lane",
        "acquisition_evidence_sha256",
    }
)
_FORBIDDEN_PUBLIC_FIELDS = frozenset(
    {
        "policy_decision",
        "evidence",
        "clearance",
        "rights",
        "acquisition_evidence",
        "processed_ref",
        "text_sha256",
        "sha256",
        "description",
        "organizer",
        "performers",
        "source_url",
        "body_ro",
        "text",
    }
)
_DURABLE_QUERY = re.compile(r"^__query_sha256__=[0-9a-f]{64}$")
_DNS_LABEL = re.compile(r"^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$")
_LEGACY_NUMERIC_LABEL = re.compile(r"^(?:0x[0-9a-f]+|[0-9]+)$")
_PRIVATE_SUFFIXES = (
    ".localhost",
    ".local",
    ".internal",
    ".home",
    ".lan",
    ".corp",
    ".private",
    ".home.arpa",
)
_COMMON_OUTPUT_FIELDS = frozenset(
    {
        "id",
        "kind",
        "title",
        "tags",
        "facets",
        "source",
        "provenance",
        "license",
        "attribution",
        "access_type",
        "legal_basis",
        "gdpr_relevant",
        "privacy_classification",
        "privacy_revision",
        "policy_decision_id",
        "redistributable",
        "confidence",
        "content_id",
        "capture_id",
        "capture_schema_version",
        "acquisition_lane",
        "acquisition_evidence_sha256",
        "policy_attestation",
    }
)
_OBSERVATION_FIELDS = frozenset({"first_seen", "last_seen", "updated_at"})
_EVENT_FACT_FIELDS = frozenset(
    {"category", "status", "lifecycle_status", "cancelled", "tombstone", "is_tombstone"}
)
_EVENT_ALIASES = frozenset({"venue_id", "place_id"})
_LIVE_EVENT_FIELDS = frozenset(
    {
        "start_datetime",
        "starts_at",
        "end_datetime",
        "ends_at",
        "timezone",
        "currency",
        "is_free",
    }
)
_OPTIONAL_LIVE_EVENT_FIELDS = frozenset(
    {"recurrence", "location", "ticket_url", "price_min", "price_max", "availability"}
)
_VENUE_FIELDS = frozenset({"location", "address", "category", "website"})
_APP_PACK_PAGE_FIELDS = frozenset(
    {
        "pack_id",
        "app",
        "layer",
        "schema_version",
        "snapshot_id",
        "release_id",
        "snapshot_generated_at",
        "snapshot_mode",
        "snapshot_complete",
        "items",
        "pagination",
        "withheld",
        "errors",
    }
)


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
    if not incoming:
        raise RoeduContractError(f"app-pack {field} is missing")
    if current and current != incoming:
        raise RoeduContractError(f"app-pack {field} changed while paging")
    return current or incoming


def require_canonical_social_pack(pack: object) -> str:
    value = str(pack or "").strip()
    if value != SOCIAL_APP_PACK_ID:
        raise RoeduContractError(f"social app-pack must be the canonical {SOCIAL_APP_PACK_ID!r}")
    return value


def _sha256(value: object) -> bool:
    return isinstance(value, str) and _SHA256.fullmatch(value) is not None


def _stable_id(value: object) -> bool:
    return bool(
        isinstance(value, str)
        and value
        and len(value) <= 128
        and value == value.strip()
        and not any(char.isspace() or ord(char) < 0x20 for char in value)
    )


def _exact_text(value: object, maximum: int, *, allow_empty: bool = False) -> bool:
    return bool(
        isinstance(value, str)
        and len(value) <= maximum
        and value == value.strip()
        and (allow_empty or bool(value))
        and not any(ord(char) < 0x20 for char in value)
    )


def _optional_text(value: object, maximum: int) -> bool:
    return value is None or _exact_text(value, maximum)


def _finite(value: object, minimum: float, maximum: float | None = None) -> bool:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return False
    try:
        number = float(value)
    except (OverflowError, ValueError):
        return False
    return bool(
        math.isfinite(number) and number >= minimum and (maximum is None or number <= maximum)
    )


def _aware_datetime(value: object) -> datetime | None:
    if not _exact_text(value, 80) or len(value) < 20 or value[10] != "T":
        return None
    try:
        parsed = datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None
    if parsed.tzinfo is None or parsed.utcoffset() is None:
        return None
    return parsed


def _public_url(value: object, *, maximum: int = 500) -> bool:
    if not _exact_text(value, maximum) or any(char.isspace() for char in value):
        return False
    try:
        parts = urllib.parse.urlsplit(str(value))
        raw_host = parts.hostname or ""
        host = raw_host.casefold()
        if (
            parts.scheme not in {"http", "https"}
            or not host
            or raw_host.endswith(".")
            or parts.username is not None
            or parts.password is not None
            or bool(parts.fragment)
            or (bool(parts.query) and _DURABLE_QUERY.fullmatch(parts.query) is None)
            or host == "localhost"
            or host.endswith(_PRIVATE_SUFFIXES)
        ):
            return False
        _ = parts.port
        try:
            return ipaddress.ip_address(host).is_global
        except ValueError:
            labels = host.split(".")
            return bool(
                len(labels) >= 2
                and len(host) <= 253
                and not all(_LEGACY_NUMERIC_LABEL.fullmatch(label) for label in labels)
                and all(_DNS_LABEL.fullmatch(label) for label in labels)
            )
    except ValueError:
        return False


def is_redistributable_app_pack_item(item: dict) -> bool:
    """Return whether an app-pack item is safe for public/app consumers.

    The serving layer is expected to enforce this first. This client repeats the
    gate so missing/unknown legal or GDPR metadata fails closed if a fixture or
    future endpoint regresses.
    """
    return (
        item.get("redistributable") is True
        and isinstance(item.get("access_type"), str)
        and item.get("access_type") in REDISTRIBUTABLE_ACCESS_TYPES
        and bool(str(item.get("legal_basis") or "").strip())
        and item.get("gdpr_relevant") is False
    )


def _policy_attestation_is_current(item: dict) -> bool:
    attestation = item.get("policy_attestation")
    return bool(
        isinstance(attestation, dict)
        and set(attestation) == _ATTESTATION_FIELDS
        and _sha256(item.get("content_id"))
        and _sha256(item.get("capture_id"))
        and item.get("capture_schema_version") == 3
        and not isinstance(item.get("capture_schema_version"), bool)
        and isinstance(item.get("acquisition_lane"), str)
        and item.get("acquisition_lane") in _ACQUISITION_LANES
        and _sha256(item.get("acquisition_evidence_sha256"))
        and _sha256(attestation.get("decision_id"))
        and _sha256(attestation.get("evidence_digest"))
        and _sha256(attestation.get("clearance_digest"))
        and attestation.get("subject_sha256") == item.get("content_id")
        and attestation.get("schema_version") == POLICY_SCHEMA_VERSION
        and attestation.get("ruleset_version") == POLICY_RULESET_VERSION
        and attestation.get("ruleset_hash") == POLICY_RULESET_HASH
        and attestation.get("action") == "publish_source"
        and attestation.get("effect") == "allow"
        and attestation.get("reasons") == ["explicitly_cleared"]
        and isinstance(attestation.get("obligations"), list)
        and all(
            isinstance(obligation, str)
            and obligation in {"attribution", "share_alike", "noncommercial", "verbatim_only"}
            for obligation in attestation["obligations"]
        )
        and attestation.get("capture_id") == item.get("capture_id")
        and attestation.get("capture_schema_version") == 3
        and isinstance(attestation.get("acquisition_lane"), str)
        and attestation.get("acquisition_lane") in _ACQUISITION_LANES
        and attestation.get("acquisition_lane") == item.get("acquisition_lane")
        and attestation.get("acquisition_evidence_sha256")
        == item.get("acquisition_evidence_sha256")
        and item.get("policy_decision_id") == attestation.get("decision_id")
    )


def _social_common_is_valid(item: dict) -> bool:
    provenance = item.get("provenance")
    return bool(
        not _FORBIDDEN_PUBLIC_FIELDS.intersection(item)
        and _stable_id(item.get("id"))
        and _exact_text(item.get("source"), 255)
        and _exact_text(item.get("license"), 120)
        and _exact_text(item.get("attribution"), 255)
        and isinstance(item.get("access_type"), str)
        and item.get("access_type") in REDISTRIBUTABLE_ACCESS_TYPES
        and _exact_text(item.get("legal_basis"), 500)
        and item.get("privacy_classification") == "no_personal_data"
        and _sha256(item.get("privacy_revision"))
        and isinstance(provenance, dict)
        and not provenance
        and _finite(item.get("confidence"), 0.0, 1.0)
        and isinstance(item.get("tags"), list)
        and all(_exact_text(tag, 128) for tag in item["tags"])
        and len(item["tags"]) == len(set(item["tags"]))
        and isinstance(item.get("facets"), dict)
        and is_redistributable_app_pack_item(item)
        and _policy_attestation_is_current(item)
    )


def _observations_are_valid(item: dict) -> bool:
    first = _aware_datetime(item.get("first_seen"))
    last = _aware_datetime(item.get("last_seen"))
    updated = _aware_datetime(item.get("updated_at"))
    return bool(first and last and updated and last >= first and updated >= first)


def _location_is_valid(value: object) -> bool:
    return bool(
        isinstance(value, dict)
        and set(value) == {"lat", "lon"}
        and _finite(value.get("lat"), -90.0, 90.0)
        and _finite(value.get("lon"), -180.0, 180.0)
    )


def _aliases_are_valid(item: dict, facets: dict) -> bool:
    venue_present = "venue_id" in item or "place_id" in item
    if venue_present and not ({"venue_id", "place_id"} <= set(item)):
        return False
    venue_id = item.get("venue_id") if venue_present else None
    return bool(
        (venue_id is None or _stable_id(venue_id))
        and (not venue_present or item.get("place_id") == venue_id)
        and facets.get("venue_id") == venue_id
        and facets.get("place_id") == venue_id
    )


def _venue_is_valid(item: dict) -> bool:
    facets = item.get("facets")
    address = item.get("address")
    category = item.get("category")
    return bool(
        set(item) == _COMMON_OUTPUT_FIELDS | _OBSERVATION_FIELDS | _VENUE_FIELDS
        and item.get("kind") == "venue"
        and _social_common_is_valid(item)
        and _observations_are_valid(item)
        and _exact_text(item.get("title"), 255)
        and isinstance(category, str)
        and _CATEGORY.fullmatch(category)
        and item.get("tags") == [f"venue:{category}"]
        and isinstance(facets, dict)
        and set(facets) == {"city", "county", "category", "venue_category", "place_category"}
        and _optional_text(facets.get("city"), 128)
        and _optional_text(facets.get("county"), 128)
        and facets.get("category") == category
        and facets.get("venue_category") == category
        and facets.get("place_category") == category
        and _location_is_valid(item.get("location"))
        and isinstance(address, dict)
        and set(address) == {"street", "city", "county", "country"}
        and _exact_text(address.get("street"), 255, allow_empty=True)
        and _exact_text(address.get("city"), 128, allow_empty=True)
        and _exact_text(address.get("county"), 128, allow_empty=True)
        and isinstance(address.get("country"), str)
        and re.fullmatch(r"[A-Z]{2}", address["country"])
        and address["city"] == (facets.get("city") or "")
        and address["county"] == (facets.get("county") or "")
        and _public_url(item.get("website"))
    )


def _commerce_is_valid(item: dict) -> bool:
    minimum = item.get("price_min")
    maximum = item.get("price_max")
    if minimum is not None and not _finite(minimum, 0.0, 9_999_999_999.99):
        return False
    if maximum is not None and not _finite(maximum, 0.0, 9_999_999_999.99):
        return False
    return bool(
        (minimum is None or maximum is None or float(maximum) >= float(minimum))
        and isinstance(item.get("currency"), str)
        and re.fullmatch(r"[A-Z]{3}", item["currency"])
        and isinstance(item.get("is_free"), bool)
        and (
            "availability" not in item
            or (
                isinstance(item.get("availability"), str)
                and item.get("availability") in _AVAILABILITY
            )
        )
        and ("ticket_url" not in item or _public_url(item.get("ticket_url")))
    )


def _event_is_valid(item: dict, *, tombstone: bool) -> bool:
    facets = item.get("facets")
    category = item.get("category")
    status = item.get("status")
    required = _COMMON_OUTPUT_FIELDS | _OBSERVATION_FIELDS | _EVENT_FACT_FIELDS
    if tombstone:
        if not (required <= set(item) <= required | _EVENT_ALIASES):
            return False
    elif not (
        required | _LIVE_EVENT_FIELDS | _EVENT_ALIASES
        <= set(item)
        <= required | _LIVE_EVENT_FIELDS | _EVENT_ALIASES | _OPTIONAL_LIVE_EVENT_FIELDS
    ):
        return False
    if not (
        _social_common_is_valid(item)
        and _observations_are_valid(item)
        and isinstance(category, str)
        and _CATEGORY.fullmatch(category)
        and isinstance(facets, dict)
        and set(facets)
        == {
            "city",
            "county",
            "category",
            "venue_id",
            "place_id",
            "status",
            "lifecycle_status",
        }
        and _optional_text(facets.get("city"), 128)
        and _optional_text(facets.get("county"), 128)
        and facets.get("category") == category
        and facets.get("status") == status
        and facets.get("lifecycle_status") == status
        and _aliases_are_valid(item, facets)
        and item.get("tags") == [f"event:{category}", f"lifecycle:{status}"]
    ):
        return False
    if tombstone:
        forbidden = {
            "start_datetime",
            "starts_at",
            "end_datetime",
            "ends_at",
            "timezone",
            "recurrence",
            "location",
            "ticket_url",
            "price_min",
            "price_max",
            "currency",
            "is_free",
            "availability",
        }
        return bool(
            item.get("kind") == "event_tombstone"
            and item.get("title") == ""
            and status == "removed"
            and item.get("lifecycle_status") == "removed"
            and item.get("cancelled") is False
            and item.get("tombstone") is True
            and item.get("is_tombstone") is True
            and not forbidden.intersection(item)
        )
    starts = _aware_datetime(item.get("start_datetime"))
    ends_raw = item.get("end_datetime")
    ends = None if ends_raw == "" else _aware_datetime(ends_raw)
    timezone_name = item.get("timezone")
    try:
        timezone_valid = (
            isinstance(timezone_name, str)
            and timezone_name == timezone_name.strip()
            and bool(ZoneInfo(timezone_name))
        )
    except (ValueError, ZoneInfoNotFoundError):
        timezone_valid = False
    return bool(
        item.get("kind") == "event"
        and _exact_text(item.get("title"), 255)
        and isinstance(status, str)
        and status in _EVENT_STATUSES
        and item.get("lifecycle_status") == status
        and item.get("cancelled") is (status == "cancelled")
        and item.get("tombstone") is False
        and item.get("is_tombstone") is False
        and starts
        and item.get("starts_at") == item.get("start_datetime")
        and (ends_raw == "" or ends)
        and item.get("ends_at") == ends_raw
        and (ends is None or ends >= starts)
        and timezone_valid
        and ("recurrence" not in item or _exact_text(item.get("recurrence"), 1000))
        and ("location" not in item or _location_is_valid(item.get("location")))
        and _commerce_is_valid(item)
    )


def is_canonical_social_app_pack_item(item: object) -> bool:
    if not isinstance(item, dict):
        return False
    if item.get("kind") == "venue":
        return _venue_is_valid(item)
    if item.get("kind") == "event":
        return _event_is_valid(item, tombstone=False)
    if item.get("kind") == "event_tombstone":
        return _event_is_valid(item, tombstone=True)
    return False


def _item_matches_requested_filters(item: dict, filters: dict) -> bool:
    facets = item.get("facets")
    city = filters.get("city")
    kind = filters.get("kind")
    tag = filters.get("tag")
    return bool(
        (city is None or (facets.get("city") or "").casefold() == city.casefold())
        and (kind is None or item.get("kind") == kind)
        and (tag is None or tag in item.get("tags", ()))
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
        app: str = SOCIAL_APP_NAME,
        layer: str = "redistributable",
        cursor: str | None = None,
        limit: int = 200,
        **filters,
    ) -> dict:
        if app != SOCIAL_APP_NAME:
            raise RoeduContractError("social client cannot read another app's pack")
        pack = require_canonical_social_pack(pack)
        if set(filters) - {"city", "kind", "tag"} or not all(
            _exact_text(value, 128) for value in filters.values()
        ):
            raise RoeduContractError("social app-pack filters are unsupported or invalid")
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
        app: str = SOCIAL_APP_NAME,
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
        result = self.read_app_pack(
            pack,
            app=app,
            layer=layer,
            limit=limit,
            max_records=max_records,
            **filters,
        )
        yield from result.items

    def read_app_pack(
        self,
        pack: str,
        *,
        app: str = SOCIAL_APP_NAME,
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

        if app != SOCIAL_APP_NAME:
            raise RoeduContractError("social client cannot read another app's pack")
        pack = require_canonical_social_pack(pack)
        if set(filters) - {"city", "kind", "tag"} or not all(
            _exact_text(value, 128) for value in filters.values()
        ):
            raise RoeduContractError("social app-pack filters are unsupported or invalid")

        cursor = None
        seen_cursors: set[str] = set()
        seen_item_ids: set[str] = set()
        items: list[dict] = []
        pack_id = snapshot_id = release_id = generated_at = snapshot_mode = ""
        declared_complete: bool | None = None
        items_valid = True
        locally_complete = False
        producer_clean = True
        truncated = False
        while True:
            page = self.app_pack_page(
                pack,
                app=app,
                layer=layer,
                cursor=cursor,
                limit=limit,
                **filters,
            )
            if (
                not isinstance(page, dict)
                or set(page) != _APP_PACK_PAGE_FIELDS
                or not isinstance(page.get("snapshot_id"), str)
                or _PROMOTED_RELEASE_ID.fullmatch(page["snapshot_id"]) is None
                or page.get("release_id") != page["snapshot_id"]
                or page.get("pack_id") != pack
                or page.get("app") != app
                or page.get("layer") != layer
                or isinstance(page.get("schema_version"), bool)
                or page.get("schema_version") != SOCIAL_APP_PACK_SCHEMA_VERSION
            ):
                raise RoeduContractError("app-pack page has the wrong product identity")
            page_generated_at = page.get("snapshot_generated_at")
            if _aware_datetime(page_generated_at) is None:
                raise RoeduContractError("app-pack snapshot_generated_at is not offset-aware")
            page_mode = page.get("snapshot_mode")
            page_complete = page.get("snapshot_complete")
            if (
                not isinstance(page_mode, str)
                or page_mode not in {"full", "partial"}
                or not isinstance(page_complete, bool)
                or page_complete is not (page_mode == "full")
            ):
                raise RoeduContractError("app-pack page has contradictory completeness")
            if declared_complete is not None and page_complete is not declared_complete:
                raise RoeduContractError("app-pack completeness changed while paging")
            declared_complete = page_complete
            pack_id = _consistent_metadata(pack_id, page.get("pack_id"), "pack_id")
            snapshot_id = _consistent_metadata(snapshot_id, page.get("snapshot_id"), "snapshot_id")
            release_id = _consistent_metadata(release_id, page.get("release_id"), "release_id")
            generated_at = _consistent_metadata(
                generated_at,
                page_generated_at,
                "snapshot_generated_at",
            )
            snapshot_mode = _consistent_metadata(snapshot_mode, page_mode, "snapshot_mode")
            if not snapshot_id or not release_id:
                raise RoeduContractError("app-pack page has incomplete release identity")
            withheld = page.get("withheld")
            errors = page.get("errors")
            if (
                isinstance(withheld, bool)
                or not isinstance(withheld, int)
                or withheld < 0
                or not isinstance(errors, list)
                or not all(isinstance(error, str) and error.strip() for error in errors)
            ):
                raise RoeduContractError("app-pack page has an invalid result envelope")
            producer_clean = producer_clean and withheld == 0 and not errors
            page_items = page.get("items")
            if not isinstance(page_items, list):
                raise RoeduContractError("app-pack page items must be a list")
            for item in page_items:
                if not is_canonical_social_app_pack_item(
                    item
                ) or not _item_matches_requested_filters(item, filters):
                    items_valid = False
                    continue
                item_id = item["id"]
                if item_id in seen_item_ids:
                    items_valid = False
                    continue
                seen_item_ids.add(item_id)
                items.append(item)
                if max_records and len(items) >= max_records:
                    truncated = True
                    break
            if truncated:
                break
            pagination = page.get("pagination")
            if not isinstance(pagination, dict) or set(pagination) != {"next_cursor"}:
                raise RoeduContractError("app-pack page has invalid pagination")
            next_cursor = pagination.get("next_cursor")
            if next_cursor is None:
                locally_complete = True
                break
            if not _exact_text(next_cursor, 4096):
                raise RoeduContractError("app-pack page has an invalid next cursor")
            if next_cursor in seen_cursors:
                raise RoeduContractError("app-pack cursor repeated while paging")
            seen_cursors.add(next_cursor)
            cursor = next_cursor

        venue_ids = {item["id"] for item in items if item.get("kind") == "venue"}
        related_items: list[dict] = []
        for item in items:
            if (
                item.get("kind") == "event"
                and item.get("venue_id")
                and item["venue_id"] not in venue_ids
            ):
                items_valid = False
                continue
            related_items.append(item)
        return AppPackRead(
            tuple(related_items),
            pack_id,
            snapshot_id,
            release_id,
            generated_at,
            snapshot_mode,
            bool(
                locally_complete
                and not truncated
                and declared_complete
                and snapshot_mode == "full"
                and producer_clean
                and items_valid
            ),
        )
