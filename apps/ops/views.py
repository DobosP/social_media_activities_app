"""Privacy-respecting observability (IS-6): a liveness/readiness probe and an
AGGREGATE-only stats endpoint. No per-user analytics, no behavioural tracking."""

from django.conf import settings
from django.db import connection
from django.db.models import Sum
from rest_framework import status
from rest_framework.permissions import AllowAny, IsAdminUser
from rest_framework.response import Response
from rest_framework.views import APIView


class HealthView(APIView):
    """Liveness + DB readiness probe for load balancers / uptime checks. Returns 503
    when the database is unreachable so orchestrators can route around the instance."""

    permission_classes = [AllowAny]
    # The probe must never be rate-limited: sharing the global anon throttle would let
    # shared-IP/proxy traffic 429 the health check and make the orchestrator flap the node.
    throttle_classes = []

    def get(self, request):
        db_ok = True
        try:
            with connection.cursor() as cursor:
                cursor.execute("SELECT 1")
                cursor.fetchone()
        except Exception:
            db_ok = False
        body = {
            "status": "ok" if db_ok else "degraded",
            "database": db_ok,
            "version": getattr(settings, "APP_VERSION", "unknown"),
        }
        code = status.HTTP_200_OK if db_ok else status.HTTP_503_SERVICE_UNAVAILABLE
        return Response(body, status=code)


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
