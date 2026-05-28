import secrets

from django.conf import settings
from django.core import signing
from rest_framework import status
from rest_framework.exceptions import PermissionDenied
from rest_framework.generics import get_object_or_404
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .identity.base import IdentityVerificationError
from .identity.providers.eudi import AGE_OVER_16, AGE_OVER_18, EUDIWalletProvider
from .models import GuardianRelationship, User
from .serializers import MeSerializer, WardSerializer
from .services import apply_assurance, is_guardian_of

# Signed-state salt binding an OpenID4VP nonce to the user who started verification.
_EUDI_STATE_SALT = "accounts.eudi.age-verification"
_EUDI_STATE_MAX_AGE = 600  # seconds

# OpenID4VP presentation definition: request only the over-threshold age booleans.
AGE_PRESENTATION_DEFINITION = {
    "id": "age-verification",
    "input_descriptors": [
        {
            "id": "age-attestation",
            "format": {"jwt_vc": {"alg": ["ES256"]}},
            "constraints": {
                "fields": [{"path": [f"$.{AGE_OVER_16}"]}, {"path": [f"$.{AGE_OVER_18}"]}]
            },
        }
    ],
}


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


class EUDIVerifyStartView(APIView):
    """Begin EUDI age verification (OpenID4VP): issue a nonce bound to this user.

    The client takes `nonce` + `audience` to the user's wallet, which returns a signed age
    attestation; the client then POSTs it (with `state`) to EUDIVerifyView."""

    permission_classes = [IsAuthenticated]

    def post(self, request):
        nonce = secrets.token_urlsafe(24)
        state = signing.dumps({"nonce": nonce, "uid": request.user.pk}, salt=_EUDI_STATE_SALT)
        return Response(
            {
                "nonce": nonce,
                "audience": settings.EUDI_CLIENT_ID,
                "state": state,
                "presentation_definition": AGE_PRESENTATION_DEFINITION,
            }
        )


class EUDIVerifyView(APIView):
    """Complete EUDI age verification: cryptographically verify the wallet's presentation
    and apply the proven age band to the user."""

    permission_classes = [IsAuthenticated]

    def post(self, request):
        try:
            payload = signing.loads(
                request.data.get("state") or "", salt=_EUDI_STATE_SALT, max_age=_EUDI_STATE_MAX_AGE
            )
        except signing.BadSignature:
            return Response(
                {"detail": "Invalid or expired verification state; restart verification."},
                status=status.HTTP_400_BAD_REQUEST,
            )
        if payload.get("uid") != request.user.pk:
            return Response(
                {"detail": "Verification state does not belong to this user."},
                status=status.HTTP_400_BAD_REQUEST,
            )

        presentation = {
            "vp_token": request.data.get("vp_token"),
            "nonce": payload["nonce"],
            "audience": settings.EUDI_CLIENT_ID,
            "method": "openid4vp",
        }
        try:
            result = EUDIWalletProvider().verify(request.user, presentation=presentation)
        except IdentityVerificationError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)

        apply_assurance(request.user, result)
        return Response(MeSerializer(request.user).data)
