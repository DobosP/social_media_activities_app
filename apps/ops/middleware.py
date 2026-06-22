"""Operational middleware."""

from django.conf import settings
from django.http import JsonResponse


class MaxBodySizeMiddleware:
    """Reject requests whose declared Content-Length exceeds MAX_REQUEST_BODY_BYTES, before
    the body is read. Django's DATA_UPLOAD_MAX_MEMORY_SIZE does not cover DRF's JSON parser
    (which reads request.body directly), so without this an unbounded JSON POST could
    exhaust the single ASGI worker's memory (a cheap DoS). 413 = Payload Too Large."""

    def __init__(self, get_response):
        self.get_response = get_response
        self.max_bytes = getattr(settings, "MAX_REQUEST_BODY_BYTES", 8 * 1024 * 1024)

    def __call__(self, request):
        length = request.META.get("CONTENT_LENGTH") or 0
        try:
            if int(length) > self.max_bytes:
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
        import re
        import uuid

        from apps.ops.observability import set_request_id

        # Allowlist a safe charset for an inbound id: a forged value with CR/LF/control chars would
        # otherwise inject into plain-text logs AND make the response-header assignment below raise
        # BadHeaderError (a 500). Anything that doesn't match is replaced with a fresh minted id.
        raw = (request.headers.get("X-Request-ID") or "")[:64].strip()
        rid = raw if re.fullmatch(r"[A-Za-z0-9._-]{1,64}", raw) else uuid.uuid4().hex
        set_request_id(rid)
        try:
            import sentry_sdk

            sentry_sdk.set_tag("request_id", rid)  # no-op when Sentry isn't configured
        except Exception:  # noqa: BLE001 — observability must never break a request
            pass
        response = self.get_response(request)
        response.headers["X-Request-ID"] = rid
        return response


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
