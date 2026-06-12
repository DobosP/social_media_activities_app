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

from .export import build_user_export
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


class MeExportView(APIView):
    """GDPR Art. 20 data portability: the authenticated user's own data as JSON.

    Returns a structured snapshot (profile, age band, cohort, consent metadata,
    memberships/activities, donations summary) — only the requester's own data."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        return Response(build_user_export(request.user))


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


class WardExportView(APIView):
    """GDPR Art. 20 data portability, guardian-for-ward variant: a verified guardian
    exports their under-age ward's data as JSON. Authorised only for an active
    guardianship link (the same gate WardDetailView uses)."""

    permission_classes = [IsAuthenticated]

    def get(self, request, public_id):
        ward = get_object_or_404(User, public_id=public_id)
        if not is_guardian_of(request.user, ward):
            raise PermissionDenied("You are not this user's guardian.")
        return Response(build_user_export(ward))


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
            # Optional holder key-binding proof (proof-of-possession of the credential
            # holder key over our audience + nonce); when present the credential is bound
            # to this account's holder id, preventing credential transfer/replay.
            "holder_binding_proof": request.data.get("holder_binding_proof"),
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


class ObtainAPIToken(APIView):
    """W10 mobile auth: exchange username+password for an opaque DRF token (Bearer-style
    `Authorization: Token <key>`). No JWT — the token is server-validated, carries no
    PII, and is revoked by deleting the row. Throttled hard (its own scope) because a
    credential endpoint is a stuffing target; counts per-IP for anonymous callers."""

    permission_classes: list = []  # credentials ARE the authentication here
    authentication_classes: list = []
    throttle_scope = "token_obtain"

    def get_throttles(self):
        from rest_framework.settings import api_settings
        from rest_framework.throttling import ScopedRateThrottle

        # Honour a config with throttling disabled (test settings empty the rates dict).
        if not (api_settings.DEFAULT_THROTTLE_RATES or {}).get(self.throttle_scope):
            return []
        return [ScopedRateThrottle()]

    def post(self, request):
        from django.contrib.auth import authenticate
        from rest_framework.authtoken.models import Token

        username = request.data.get("username") or ""
        password = request.data.get("password") or ""
        user = authenticate(request, username=username, password=password)
        if user is None or not user.is_active:
            return Response({"detail": "Invalid credentials."}, status=status.HTTP_400_BAD_REQUEST)
        token, _ = Token.objects.get_or_create(user=user)
        return Response({"token": token.key})

    def delete(self, request):
        """Revoke the caller's token (mobile logout). Requires the token itself."""
        from rest_framework.authentication import TokenAuthentication
        from rest_framework.authtoken.models import Token

        auth = TokenAuthentication().authenticate(request)
        if auth is None:
            return Response(status=status.HTTP_401_UNAUTHORIZED)
        user, _ = auth
        Token.objects.filter(user=user).delete()
        return Response(status=status.HTTP_204_NO_CONTENT)


class MeSettingsView(APIView):
    """W10: the user's own preference panel for API clients — notification mutes +
    stated access needs. Strictly self-scoped; MODERATION/SYSTEM notices are never
    mutable (enforced by set_muted_kinds, same as the web form)."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        from apps.notifications.services import get_muted_kinds
        from apps.places.services import get_access_preference

        pref = get_access_preference(request.user)
        return Response(
            {
                "muted_kinds": sorted(get_muted_kinds(request.user)),
                "access": {
                    "needs_step_free": bool(pref and pref.needs_step_free),
                    "needs_accessible_toilet": bool(pref and pref.needs_accessible_toilet),
                    "prefers_quiet": bool(pref and pref.prefers_quiet),
                },
            }
        )

    def put(self, request):
        from apps.notifications.services import set_muted_kinds
        from apps.places.services import set_access_preference

        if "muted_kinds" in request.data:
            kinds = request.data.get("muted_kinds")
            if not isinstance(kinds, list):
                return Response(
                    {"detail": "muted_kinds must be a list."},
                    status=status.HTTP_400_BAD_REQUEST,
                )
            set_muted_kinds(request.user, kinds)
        access = request.data.get("access")
        if access is not None:
            if not isinstance(access, dict):
                return Response(
                    {"detail": "access must be an object."}, status=status.HTTP_400_BAD_REQUEST
                )
            set_access_preference(
                request.user,
                needs_step_free=bool(access.get("needs_step_free")),
                needs_accessible_toilet=bool(access.get("needs_accessible_toilet")),
                prefers_quiet=bool(access.get("prefers_quiet")),
            )
        return self.get(request)
