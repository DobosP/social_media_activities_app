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
