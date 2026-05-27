from rest_framework.exceptions import PermissionDenied
from rest_framework.generics import get_object_or_404
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .models import GuardianRelationship, User
from .serializers import MeSerializer, WardSerializer
from .services import is_guardian_of


class MeView(APIView):
    """Current user's profile, age band, cohort and participation status."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        return Response(MeSerializer(request.user).data)


class WardListView(APIView):
    """The minors this user is the parent/legal guardian of."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        wards = User.objects.filter(
            guardians__guardian=request.user,
            guardians__status=GuardianRelationship.Status.ACTIVE,
        ).distinct()
        return Response(WardSerializer(wards, many=True).data)


class WardDetailView(APIView):
    """A guardian reads or manages one of their wards' accounts (e.g. display name)."""

    permission_classes = [IsAuthenticated]

    def _get_ward(self, request, public_id):
        ward = get_object_or_404(User, public_id=public_id)
        if not is_guardian_of(request.user, ward):
            raise PermissionDenied("You are not this user's guardian.")
        return ward

    def get(self, request, public_id):
        return Response(WardSerializer(self._get_ward(request, public_id)).data)

    def patch(self, request, public_id):
        ward = self._get_ward(request, public_id)
        serializer = WardSerializer(ward, data=request.data, partial=True)
        serializer.is_valid(raise_exception=True)
        serializer.save()
        return Response(serializer.data)
