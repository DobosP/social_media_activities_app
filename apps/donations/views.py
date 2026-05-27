from django.conf import settings
from rest_framework import status
from rest_framework.exceptions import PermissionDenied, ValidationError
from rest_framework.permissions import AllowAny, IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import Donation
from .serializers import (
    DonationSerializer,
    DonationWebhookSerializer,
    StartDonationSerializer,
)
from .services import DonationError, complete_donation, completed_total_cents, start_donation


class StartDonationView(APIView):
    """Begin a donation (anonymous allowed). Returns the donation and a checkout URL."""

    permission_classes = [AllowAny]

    def post(self, request):
        serializer = StartDonationSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        try:
            donation, checkout_url = start_donation(
                request.user,
                data["amount_cents"],
                data["currency"],
                recurring=data["recurring"],
            )
        except DonationError as exc:
            raise ValidationError(str(exc)) from exc
        body = DonationSerializer(donation).data
        body["checkout_url"] = checkout_url
        return Response(body, status=status.HTTP_201_CREATED)


class MyDonationsView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        donations = Donation.objects.filter(donor=request.user).order_by("-created_at")
        return Response(DonationSerializer(donations, many=True).data)


class DonationTotalView(APIView):
    """Public transparency figure: total completed donations (aggregate, no PII)."""

    permission_classes = [AllowAny]

    def get(self, request):
        currency = request.query_params.get("currency", "EUR")
        return Response({"currency": currency, "total_cents": completed_total_cents(currency)})


class DonationWebhookView(APIView):
    """Provider callback that marks a donation completed. Guarded by a shared secret
    (`DONATIONS_WEBHOOK_SECRET`) supplied in the `X-Webhook-Secret` header when set."""

    permission_classes = [AllowAny]

    def post(self, request):
        secret = getattr(settings, "DONATIONS_WEBHOOK_SECRET", "")
        if secret and request.headers.get("X-Webhook-Secret") != secret:
            raise PermissionDenied("Invalid webhook secret.")
        serializer = DonationWebhookSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        donation = complete_donation(serializer.validated_data["external_ref"])
        if donation is None:
            return Response({"status": "ignored"}, status=status.HTTP_200_OK)
        return Response(DonationSerializer(donation).data)
