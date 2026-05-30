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
from .webhooks import constant_time_secret_ok, verify_stripe_signature


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
                campaign=data.get("campaign"),
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
    """Provider callback that marks a donation completed.

    Authenticated and **fail-closed**: when the Stripe provider + `STRIPE_WEBHOOK_SECRET`
    are configured, a real Stripe Event is accepted only with a valid `Stripe-Signature`;
    otherwise a constant-time shared-secret check against `DONATIONS_WEBHOOK_SECRET` (sent
    in `X-Webhook-Secret`) is required. With neither configured the endpoint rejects every
    request, so a pending donation can never be forged complete by an anonymous caller."""

    permission_classes = [AllowAny]

    def _stripe_mode(self) -> bool:
        return "Stripe" in getattr(settings, "DONATIONS_PROVIDER", "") and bool(
            getattr(settings, "STRIPE_WEBHOOK_SECRET", "")
        )

    def post(self, request):
        if self._stripe_mode():
            return self._handle_stripe(request)
        return self._handle_shared_secret(request)

    def _handle_shared_secret(self, request):
        shared = getattr(settings, "DONATIONS_WEBHOOK_SECRET", "")
        if not constant_time_secret_ok(request.headers.get("X-Webhook-Secret", ""), shared):
            raise PermissionDenied("Webhook authentication failed.")
        serializer = DonationWebhookSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        return self._complete(serializer.validated_data["external_ref"])

    def _handle_stripe(self, request):
        secret = getattr(settings, "STRIPE_WEBHOOK_SECRET", "")
        if not verify_stripe_signature(
            request.body, request.headers.get("Stripe-Signature", ""), secret
        ):
            raise PermissionDenied("Webhook authentication failed.")
        # A genuine Stripe Event is nested; the donation's external_ref is the Checkout
        # Session id. Only act on a completed checkout; ignore every other event type.
        event = request.data if isinstance(request.data, dict) else {}
        if event.get("type") != "checkout.session.completed":
            return Response({"status": "ignored"}, status=status.HTTP_200_OK)
        session = (event.get("data") or {}).get("object") or {}
        external_ref = session.get("id", "")
        if not external_ref:
            return Response({"status": "ignored"}, status=status.HTTP_200_OK)
        return self._complete(external_ref)

    def _complete(self, external_ref):
        donation = complete_donation(external_ref)
        if donation is None:
            return Response({"status": "ignored"}, status=status.HTTP_200_OK)
        return Response(DonationSerializer(donation).data)
