"""Operational middleware."""

import logging
import re
import time
import uuid

from django.conf import settings
from django.http import JsonResponse

_CONTROL_CHARS = re.compile(r"[\x00-\x1f\x7f]")
_REQUEST_ID_RE = re.compile(r"[A-Za-z0-9._-]{1,64}")


class MaxBodySizeMiddleware:
    """Reject requests whose declared Content-Length exceeds MAX_REQUEST_BODY_BYTES, before
    the body is read. Django's DATA_UPLOAD_MAX_MEMORY_SIZE does not cover DRF's JSON parser
    (which reads request.body directly), so without this an unbounded JSON POST could
    exhaust the single ASGI worker's memory (a cheap DoS). 413 = Payload Too Large.

    ADR-0026: when video attachments are enabled, multipart POSTs to the thread-post endpoint
    (the only surface that accepts a video) of an activity whose cohort may carry video get a
    larger cap — MEDIA_VIDEO_MAX_UPLOAD_BYTES plus multipart overhead. The cohort lookup (one
    indexed pk query) keeps the global cap on minor-cohort threads, so the larger pre-auth
    disk-spool window exists only where a video upload is actually possible (review finding).
    The global cap is unchanged for every other request; Django spools big multipart parts to
    a temp file, so the larger cap never means a big in-memory body."""

    # The registered route carries a trailing slash (apps/web/urls.py) and APPEND_SLASH never
    # redirects a POST, so real traffic always arrives WITH the slash; the bare form is kept
    # for defence against a proxy normalising it away.
    _VIDEO_UPLOAD_PATH = re.compile(r"^/activities/(\d+)/post/?$")
    _MULTIPART_OVERHEAD = 2 * 1024 * 1024

    def __init__(self, get_response):
        self.get_response = get_response
        self.max_bytes = getattr(settings, "MAX_REQUEST_BODY_BYTES", 8 * 1024 * 1024)

    def _limit_for(self, request) -> int:
        match = None
        if (
            request.method == "POST"
            and getattr(settings, "MEDIA_VIDEO_ENABLED", False)
            and request.content_type == "multipart/form-data"
        ):
            match = self._VIDEO_UPLOAD_PATH.match(request.path)
        if match and self._cohort_accepts_video(match.group(1)):
            video_cap = (
                getattr(settings, "MEDIA_VIDEO_MAX_UPLOAD_BYTES", 80 * 1024 * 1024)
                + self._MULTIPART_OVERHEAD
            )
            return max(self.max_bytes, video_cap)
        return self.max_bytes

    @staticmethod
    def _cohort_accepts_video(activity_pk: str) -> bool:
        from apps.social.models import Activity

        cohort = (
            Activity.objects.filter(pk=int(activity_pk)).values_list("cohort", flat=True).first()
        )
        return cohort in set(getattr(settings, "MEDIA_VIDEO_COHORTS", ["adult"]))

    def __call__(self, request):
        length = request.META.get("CONTENT_LENGTH") or 0
        try:
            if int(length) > self._limit_for(request):
                return JsonResponse({"detail": "Request body too large."}, status=413)
        except (TypeError, ValueError):
            pass
        return self.get_response(request)


class RequestIDMiddleware:
    """Assign/propagate an X-Request-ID per request (P1 observability): trust a bounded inbound id
    from the proxy or mint a random one, store it in the request-id ContextVar (woven into logs),
    echo it on the response, and tag the Sentry scope so an error links to the request's logs. The
    id is random — never PII."""

    def __init__(self, get_response):
        self.get_response = get_response

    def __call__(self, request):
        from apps.ops.observability import reset_request_id, set_request_id

        # Allowlist a safe charset for an inbound id: a forged value with CR/LF/control chars would
        # otherwise inject into plain-text logs AND make the response-header assignment below raise
        # BadHeaderError (a 500). Anything that doesn't match is replaced with a fresh minted id.
        raw = (request.headers.get("X-Request-ID") or "")[:64].strip()
        rid = raw if _REQUEST_ID_RE.fullmatch(raw) else uuid.uuid4().hex
        token = set_request_id(rid)
        try:
            import sentry_sdk

            sentry_sdk.set_tag("request_id", rid)  # no-op when Sentry isn't configured
        except Exception:  # noqa: BLE001 — observability must never break a request
            pass
        try:
            response = self.get_response(request)
            response.headers["X-Request-ID"] = rid
            return response
        finally:
            reset_request_id(token)


class RequestLogMiddleware:
    """Emit one PII-safe operational request log after each response.

    The log deliberately excludes query strings, request/response bodies, headers, cookies, users,
    and IP addresses. It carries only method, sanitized path/route, status, duration, and the
    request id injected by RequestIDMiddleware.
    """

    def __init__(self, get_response):
        self.get_response = get_response
        self.enabled = getattr(settings, "REQUEST_LOGGING_ENABLED", True)
        self.logger = logging.getLogger("apps.ops.request")

    def __call__(self, request):
        started = time.monotonic()
        try:
            response = self.get_response(request)
        except Exception:
            if self.enabled:
                self._log(request, 500, started, failed=True)
            raise
        if self.enabled:
            self._log(request, response.status_code, started)
        return response

    def _log(self, request, status_code: int, started: float, *, failed: bool = False) -> None:
        from apps.ops.observability import get_request_id

        duration_ms = int((time.monotonic() - started) * 1000)
        resolver_match = getattr(request, "resolver_match", None)
        route = getattr(resolver_match, "route", "") if resolver_match else ""
        extra = {
            "request_id": get_request_id(),
            "method": request.method,
            "path": _safe_log_value(getattr(request, "path_info", ""), 240),
            "route": _safe_log_value(route or "-", 240),
            "status_code": status_code,
            "duration_ms": duration_ms,
        }
        if failed:
            self.logger.exception("request_failed", extra=extra)
        else:
            self.logger.info("request", extra=extra)


def _safe_log_value(value: str, limit: int) -> str:
    return _CONTROL_CHARS.sub(" ", str(value or ""))[:limit]


class PermissionsPolicyMiddleware:
    """Emit a Permissions-Policy header (P1 hardening). Django has settings for the other security
    headers (nosniff / referrer / HSTS) but none for Permissions-Policy, so we set it here. The
    default locks down powerful features the app never uses server-side and scopes geolocation to
    self (the web UI uses request-only proximity). Override via PERMISSIONS_POLICY."""

    DEFAULT = "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()"

    def __init__(self, get_response):
        self.get_response = get_response
        self.value = getattr(settings, "PERMISSIONS_POLICY", self.DEFAULT)
        # Reporting-Endpoints (modern Reporting API): `name="url", ...` so the CSP `report-to csp`
        # directive resolves to /ops/csp-report/. Computed once at startup.
        endpoints = getattr(settings, "CSP_REPORTING_ENDPOINTS", {}) or {}
        self.reporting_endpoints = ", ".join(f'{name}="{url}"' for name, url in endpoints.items())

    def __call__(self, request):
        response = self.get_response(request)
        if self.value:
            response.setdefault("Permissions-Policy", self.value)
        if self.reporting_endpoints:
            response.setdefault("Reporting-Endpoints", self.reporting_endpoints)
        return response
