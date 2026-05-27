from django.shortcuts import get_object_or_404
from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.places.models import Place

from .models import Booking
from .providers.base import BookingError
from .registry import get_booking_provider
from .serializers import BookingSerializer, CreateBookingSerializer
from .services import BookingDenied, booking_options, cancel_booking, create_booking


class BookingOptionsView(APIView):
    """GET /api/booking/options/?place=<id> — how to book a place."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        place = get_object_or_404(Place, pk=request.query_params.get("place"))
        return Response(booking_options(place))


class BookingViewSet(viewsets.ReadOnlyModelViewSet):
    """List/retrieve the current user's bookings; create and cancel."""

    permission_classes = [IsAuthenticated]
    serializer_class = BookingSerializer

    def get_queryset(self):
        return Booking.objects.filter(user=self.request.user).select_related("place", "activity")

    def create(self, request):
        form = CreateBookingSerializer(data=request.data)
        form.is_valid(raise_exception=True)
        data = form.validated_data
        try:
            booking = create_booking(
                request.user,
                place=data["place"],
                starts_at=data["starts_at"],
                ends_at=data.get("ends_at"),
                party_size=data.get("party_size", 1),
                activity=data.get("activity"),
                provider=data.get("provider") or None,
            )
        except BookingDenied as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_403_FORBIDDEN)
        except (BookingError, KeyError) as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_502_BAD_GATEWAY)
        return Response(BookingSerializer(booking).data, status=status.HTTP_201_CREATED)

    @action(detail=True, methods=["post"])
    def cancel(self, request, pk=None):
        booking = self.get_object()
        try:
            cancel_booking(request.user, booking)
        except BookingDenied as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_403_FORBIDDEN)
        except BookingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_502_BAD_GATEWAY)
        return Response(BookingSerializer(booking).data)


class BookingProvidersView(APIView):
    """GET /api/booking/providers/ — list available provider slugs + capability."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        from .registry import _provider_classes

        providers = []
        for slug in sorted(_provider_classes()):
            providers.append(
                {"slug": slug, "supports_realtime": get_booking_provider(slug).supports_realtime}
            )
        return Response(providers)
