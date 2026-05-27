from rest_framework import status
from rest_framework.exceptions import ValidationError
from rest_framework.permissions import AllowAny, IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import Donation
from .serializers import DonationSerializer, StartDonationSerializer
from .services import DonationError, start_donation


class StartDonationView(APIView):
    """Begin a donation (anonymous allowed). Returns the donation and a checkout URL."""

    permission_classes = [AllowAny]

    def post(self, request):
        serializer = StartDonationSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        try:
            donation, checkout_url = start_donation(
                request.user, data["amount_cents"], data["currency"]
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
