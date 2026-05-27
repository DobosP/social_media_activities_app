"""Privacy-respecting observability (IS-6): a liveness/readiness probe and an
AGGREGATE-only stats endpoint. No per-user analytics, no behavioural tracking."""

from django.db import connection
from django.db.models import Sum
from rest_framework.permissions import AllowAny, IsAdminUser
from rest_framework.response import Response
from rest_framework.views import APIView


class HealthView(APIView):
    """Liveness + DB readiness probe for load balancers / uptime checks."""

    permission_classes = [AllowAny]

    def get(self, request):
        db_ok = True
        try:
            with connection.cursor() as cursor:
                cursor.execute("SELECT 1")
                cursor.fetchone()
        except Exception:
            db_ok = False
        return Response({"status": "ok" if db_ok else "degraded", "database": db_ok})


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
