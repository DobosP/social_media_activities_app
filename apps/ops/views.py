"""Privacy-respecting observability (IS-6): a liveness/readiness probe and an
AGGREGATE-only stats endpoint. No per-user analytics, no behavioural tracking."""

import logging

from django.conf import settings
from django.core.cache import cache
from django.db import connection
from django.db.models import Sum
from rest_framework import status
from rest_framework.permissions import AllowAny, IsAdminUser
from rest_framework.response import Response
from rest_framework.views import APIView


class HealthView(APIView):
    """Cheap liveness probe for load balancers / uptime checks.

    It deliberately avoids dependency checks; use /readyz for DB/cache/storage readiness.
    """

    permission_classes = [AllowAny]
    # The probe must never be rate-limited: sharing the global anon throttle would let
    # shared-IP/proxy traffic 429 the health check and make the orchestrator flap the node.
    throttle_classes = []

    def get(self, request):
        return Response({"status": "ok", "version": getattr(settings, "APP_VERSION", "unknown")})


class ReadyView(APIView):
    """Readiness probe (P1): liveness (process up) PLUS every CONFIGURED shared dependency — the
    DB, the cache/channel-layer (when Redis-backed), and object storage (when S3). Returns 503 if a
    configured dep is down so an orchestrator drains the node instead of routing into a half-broken
    instance (on which cross-process chat fan-out + rate-limiting would silently fail). /healthz
    stays a pure liveness probe; point the readiness/health check here on a scaled-out deploy."""

    permission_classes = [AllowAny]
    throttle_classes = []  # never rate-limit a probe (would flap the node)

    def get(self, request):
        checks = {"database": self._check_db()}
        # Cache: only meaningful as a readiness gate when it is the SHARED Redis backend; the
        # per-process LocMem fallback is always "up" and not a cross-instance dependency.
        if getattr(settings, "REDIS_URL", ""):
            checks["cache"] = self._check_cache()
        # Object storage: only when the S3 backend is selected (Local is the filesystem).
        if getattr(settings, "MEDIA_STORAGE_BACKEND", "").endswith("S3StorageBackend"):
            checks["storage"] = self._check_storage()
        ready = all(checks.values())
        code = status.HTTP_200_OK if ready else status.HTTP_503_SERVICE_UNAVAILABLE
        return Response({"status": "ready" if ready else "degraded", **checks}, status=code)

    def _check_db(self) -> bool:
        try:
            with connection.cursor() as cursor:
                cursor.execute("SELECT 1")
                cursor.fetchone()
            return True
        except Exception:
            return False

    def _check_cache(self) -> bool:
        from django.core.cache import cache

        try:
            cache.set("readyz", "1", 5)
            return cache.get("readyz") == "1"
        except Exception:
            return False

    def _check_storage(self) -> bool:
        from apps.media.storage import get_storage

        try:
            # A cheap negative existence check round-trips to the bucket without writing.
            get_storage().exists("__readyz_probe__")
            return True
        except Exception:
            return False


def metrics_view(request):
    """Prometheus exposition (P1 observability) — request latency/count etc. collected by the
    django_prometheus middleware. GATED on a bearer token (METRICS_TOKEN), CLOSED BY DEFAULT (empty
    token => 403) so the endpoint is never world-readable. The scraper sends
    ``Authorization: Bearer <METRICS_TOKEN>``. A plain Django view (not DRF) so the Prometheus
    exposition response passes through unmodified. NOTE: django_prometheus counters are PER-PROCESS,
    so on a multi-worker deploy scrape every instance (or aggregate) — same caveat as the cache /
    channel-layer shared-state note."""
    import hmac

    from django.http import HttpResponseForbidden

    token = getattr(settings, "METRICS_TOKEN", "")
    provided = request.headers.get("Authorization", "")
    # Constant-time compare so the token can't be recovered by timing the response.
    if not token or not hmac.compare_digest(provided.encode(), f"Bearer {token}".encode()):
        return HttpResponseForbidden("metrics unavailable")
    from django_prometheus.exports import ExportToDjangoView

    return ExportToDjangoView(request)


class StatsView(APIView):
    """Aggregate counts only — never PII or per-user data. Staff-only."""

    permission_classes = [IsAdminUser]

    def get(self, request):
        from django.contrib.auth import get_user_model

        from apps.booking.models import Booking
        from apps.donations.models import Donation
        from apps.social.models import Activity, Post

        completed = Donation.objects.filter(status=Donation.Status.COMPLETED)
        return Response(
            {
                "users": get_user_model().objects.count(),
                "activities": Activity.objects.count(),
                "posts": Post.objects.count(),
                "bookings": Booking.objects.count(),
                "donations_completed": completed.count(),
                "donations_total_cents": completed.aggregate(s=Sum("amount_cents"))["s"] or 0,
            }
        )


_csp_logger = logging.getLogger("apps.ops.csp_report")


class CSPReportView(APIView):
    """Collects browser CSP violation reports for the report-only policy — the path to actually
    ENFORCING CSP (today the policy is report-only with nowhere to send violations).

    AllowAny + never throttled: browsers POST these unauthenticated and cross-context, and a dropped
    report is a lost signal — so the endpoint ALWAYS returns 204. The raw body is read directly (the
    ``application/csp-report`` / ``application/reports+json`` content types aren't DRF-parsed),
    bounded, and only OPERATIONAL fields (directive / blocked-uri / document-uri — the app's own
    URLs, never user PII) are logged, at a GLOBAL per-minute budget so a malicious flood of POSTs
    can't balloon the logs."""

    permission_classes = [AllowAny]
    throttle_classes = []  # like /healthz: an unauthenticated browser-driven endpoint, never 429
    # No authenticators: a browser may post the report WITH the session cookie, and DRF's
    # SessionAuthentication would then enforce CSRF (no token on a browser report) and 403 it.
    authentication_classes = []

    _MAX_BODY = 8 * 1024
    _LOG_BUDGET = 120  # log at most this many reports per minute (global); excess is still 204'd

    def post(self, request):
        if self._log_allowed():
            self._log(request.body[: self._MAX_BODY])
        return Response(status=status.HTTP_204_NO_CONTENT)

    def _log_allowed(self) -> bool:
        try:
            n = cache.get_or_set("csp-report-log-budget", 0, 60)
            if n >= self._LOG_BUDGET:
                return False
            try:
                cache.incr("csp-report-log-budget")
            except ValueError:
                cache.set("csp-report-log-budget", 1, 60)
        except Exception:
            return True  # a cache hiccup must never silence a real violation report
        return True

    def _log(self, body: bytes) -> None:
        from apps.ops.csp import parse_csp_report

        try:
            violations = parse_csp_report(body)
        except ValueError:
            _csp_logger.info("CSP report (unparseable, %d bytes)", len(body))
            return
        for violation in violations:
            _csp_logger.info(
                "CSP violation: directive=%s blocked=%s doc=%s",
                violation.directive,
                violation.blocked,
                violation.document,
            )
