"""Bounded API pagination helpers (P1).

DRF viewsets use ``BoundedLimitOffsetPagination`` globally. Several high-traffic endpoints are
plain ``APIView`` methods, so they need an explicit helper that keeps the canonical ``/api/v1/``
contract bounded without changing the transitional ``/api/`` alias shape.
"""

from django.conf import settings
from django.core import signing
from rest_framework.pagination import LimitOffsetPagination
from rest_framework.response import Response


class BoundedLimitOffsetPagination(LimitOffsetPagination):
    max_limit = 200


DEFAULT_CURSOR_LIMIT = 50
MAX_CURSOR_LIMIT = 200
_CURSOR_SALT = "ops.api-cursor-v1"


def is_versioned_api_request(request) -> bool:
    """True for the canonical API base. The unversioned /api/ alias stays compatibility-shaped."""
    return request.path_info.startswith("/api/v1/")


def parse_limit(request, *, default: int | None = None, max_limit: int | None = None) -> int:
    default = default or getattr(settings, "REST_FRAMEWORK", {}).get(
        "PAGE_SIZE", DEFAULT_CURSOR_LIMIT
    )
    max_limit = max_limit or MAX_CURSOR_LIMIT
    try:
        requested = int(request.query_params.get("limit", default))
    except (TypeError, ValueError):
        requested = default
    return max(1, min(requested, max_limit))


def _decode_cursor(value: str) -> int:
    if not value:
        return 0
    try:
        payload = signing.loads(value, salt=_CURSOR_SALT)
    except signing.BadSignature:
        return 0
    try:
        return max(0, int(payload.get("offset", 0)))
    except (TypeError, ValueError):
        return 0


def _encode_cursor(offset: int) -> str:
    return signing.dumps({"offset": max(0, int(offset))}, salt=_CURSOR_SALT)


def cursor_page(request, rows, *, default_limit: int | None = None, max_limit: int | None = None):
    """Return ``(page_items, next_cursor, limit)`` using a signed offset cursor.

    The helper slices querysets before materialising them; for already-materialised lists it slices
    in memory. It deliberately does not issue a ``COUNT(*)``.
    """
    limit = parse_limit(request, default=default_limit, max_limit=max_limit)
    offset = _decode_cursor(request.query_params.get("cursor", ""))
    window = list(rows[offset : offset + limit + 1])
    has_next = len(window) > limit
    page = window[:limit]
    next_cursor = _encode_cursor(offset + limit) if has_next else ""
    return page, next_cursor, limit


def cursor_response(
    request,
    rows,
    serializer_class,
    *,
    context: dict | None = None,
    default_limit: int | None = None,
    max_limit: int | None = None,
    extra: dict | None = None,
) -> Response:
    page, next_cursor, limit = cursor_page(
        request, rows, default_limit=default_limit, max_limit=max_limit
    )
    body = {
        "next_cursor": next_cursor,
        "limit": limit,
        "results": serializer_class(page, many=True, context=context or {}).data,
    }
    if extra:
        body = {**extra, **body}
    return Response(body)
