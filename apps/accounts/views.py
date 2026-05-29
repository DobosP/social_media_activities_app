import secrets

from django.conf import settings
from django.core import signing
from django.db import IntegrityError, transaction
from rest_framework import status
from rest_framework.exceptions import PermissionDenied
from rest_framework.generics import get_object_or_404
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from .identity.base import IdentityVerificationError
from .identity.providers.eudi import AGE_OVER_16, AGE_OVER_18, EUDIWalletProvider
from .models import ConsumedAgeNonce, GuardianRelationship, User
from .serializers import GuardianLinkInviteSerializer, MeSerializer, WardSerializer
from .services import (
    accept_guardian_link_invite,
    apply_assurance,
    create_guardian_link_invite,
    decline_guardian_link_invite,
    erase_user,
    grant_parental_consent,
    is_guardian_of,
    pending_guardian_invites_for,
    revoke_parental_consent,
)

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

    def delete(self, request):
        """GDPR Art.17 self-erasure: permanently delete the caller's own account."""
        erase_user(request.user, request.user)
        return Response(status=status.HTTP_204_NO_CONTENT)


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

    def delete(self, request, public_id):
        """GDPR Art.17 erasure on behalf of a minor: a guardian deletes their ward's
        account permanently."""
        ward = self._get_ward(request, public_id)
        erase_user(request.user, ward)
        return Response(status=status.HTTP_204_NO_CONTENT)


class WardConsentView(APIView):
    """A guardian grants (POST) or revokes (DELETE) parental consent for an under-16 ward.

    This is the self-service path the consent gate (can_participate) depends on; before
    it existed, only Django admin could create a consent record, blocking minor onboarding.
    Establishing the guardianship link itself still flows through the verified
    parental-consent identity process (see docs/COMPLIANCE.md)."""

    permission_classes = [IsAuthenticated]

    def post(self, request, public_id):
        ward = get_object_or_404(User, public_id=public_id)
        try:
            grant_parental_consent(request.user, ward)
        except ValueError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(WardSerializer(ward).data, status=status.HTTP_201_CREATED)

    def delete(self, request, public_id):
        ward = get_object_or_404(User, public_id=public_id)
        try:
            revoke_parental_consent(request.user, ward)
        except ValueError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(status=status.HTTP_204_NO_CONTENT)


class GuardianLinkView(APIView):
    """Establish a guardianship link via a mutually-confirmed invite.

    POST (a verified adult): invite a minor `ward` (by `public_id`) to confirm a link —
    returns the invite incl. the `token` the ward uses to accept.
    GET (the ward): list pending invites awaiting this user's response.
    Acceptance creates the `GuardianRelationship`; the guardian can then grant parental
    consent (WardConsentView) to make the minor eligible to participate."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        invites = pending_guardian_invites_for(request.user)
        return Response(GuardianLinkInviteSerializer(invites, many=True).data)

    def post(self, request):
        from apps.safety.services import allow_action

        if not allow_action(
            request.user,
            "guardian_invite",
            limit=getattr(settings, "GUARDIAN_INVITE_RATE_LIMIT", 20),
            window_seconds=getattr(settings, "GUARDIAN_INVITE_RATE_WINDOW_SECONDS", 3600),
        ):
            return Response(
                {"detail": "Too many invites; try again later."},
                status=status.HTTP_429_TOO_MANY_REQUESTS,
            )
        ward = get_object_or_404(User, public_id=request.data.get("ward"))
        try:
            invite = create_guardian_link_invite(
                request.user, ward, relationship=request.data.get("relationship", "parent")
            )
        except ValueError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(GuardianLinkInviteSerializer(invite).data, status=status.HTTP_201_CREATED)


class GuardianLinkAcceptView(APIView):
    """The invited ward accepts a pending guardianship invite (by token)."""

    permission_classes = [IsAuthenticated]

    def post(self, request, token):
        try:
            accept_guardian_link_invite(request.user, token)
        except ValueError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(MeSerializer(request.user).data)


class GuardianLinkDeclineView(APIView):
    """The invited ward declines a pending guardianship invite (by token)."""

    permission_classes = [IsAuthenticated]

    def post(self, request, token):
        try:
            decline_guardian_link_invite(request.user, token)
        except ValueError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(status=status.HTTP_204_NO_CONTENT)


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

        # Single-use nonce (W2-9): even a validly-signed presentation can only be redeemed
        # once. Claim the nonce after signature verification; a duplicate means replay. The
        # create() runs in a savepoint so the IntegrityError on a replay rolls back only the
        # claim, not any surrounding transaction (ATOMIC_REQUESTS / test transaction).
        try:
            with transaction.atomic():
                ConsumedAgeNonce.objects.create(nonce=payload["nonce"])
        except IntegrityError:
            return Response(
                {"detail": "This verification has already been used."},
                status=status.HTTP_400_BAD_REQUEST,
            )

        apply_assurance(request.user, result)
        return Response(MeSerializer(request.user).data)
