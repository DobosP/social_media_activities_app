from django.contrib.auth import get_user_model
from django.shortcuts import get_object_or_404
from rest_framework import status
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from . import services
from .models import Conversation, Message
from .serializers import (
    ConversationSerializer,
    MessageSerializer,
    OwnPublicKeySerializer,
    PublicKeySerializer,
)

User = get_user_model()


def _conversation_payload(conv, request):
    conv = Conversation.objects.prefetch_related("participants__user").get(pk=conv.pk)
    return ConversationSerializer(conv, context={"request": request}).data


class KeyRegistryView(APIView):
    """GET your own key (incl. the opaque backup blob); POST to publish/rotate it."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        key = services.public_key_for(request.user)
        if not key:
            return Response({"detail": "No key registered."}, status=status.HTTP_404_NOT_FOUND)
        return Response(OwnPublicKeySerializer(key).data)

    def post(self, request):
        try:
            key = services.register_public_key(
                request.user,
                request.data.get("public_jwk"),
                algorithm=request.data.get("algorithm", "ECDH-P256"),
                wrapped_private_jwk=request.data.get("wrapped_private_jwk"),
            )
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(OwnPublicKeySerializer(key).data, status=status.HTTP_201_CREATED)


class UserKeyView(APIView):
    """Fetch another user's public key — only when you're allowed to contact them
    (same cohort, not blocked). 404 (not 403) so the registry leaks nothing about
    who exists in other cohorts."""

    permission_classes = [IsAuthenticated]

    def get(self, request, username):
        target = get_object_or_404(User, username=username)
        if not services.can_message(request.user, target):
            return Response({"detail": "Not found."}, status=status.HTTP_404_NOT_FOUND)
        key = services.public_key_for(target)
        if not key:
            return Response(
                {"detail": "This user hasn't set up secure messaging yet."},
                status=status.HTTP_404_NOT_FOUND,
            )
        return Response(PublicKeySerializer(key).data)


class ConversationListCreateView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        convs = services.conversations_for(request.user).prefetch_related("participants__user")
        return Response(ConversationSerializer(convs, many=True, context={"request": request}).data)

    def post(self, request):
        kind = request.data.get("kind", Conversation.Kind.DIRECT)
        usernames = request.data.get("usernames") or []
        if isinstance(usernames, str):
            usernames = [usernames]
        single = request.data.get("username")
        if single and not usernames:
            usernames = [single]
        usernames = [u for u in dict.fromkeys(usernames) if u]
        if not usernames:
            return Response(
                {"detail": "At least one recipient username is required."},
                status=status.HTTP_400_BAD_REQUEST,
            )
        targets = list(User.objects.filter(username__in=usernames))
        if len(targets) != len(usernames):
            return Response(
                {"detail": "One or more recipients were not found."},
                status=status.HTTP_400_BAD_REQUEST,
            )
        try:
            if kind == Conversation.Kind.GROUP:
                conv = services.start_group(
                    request.user, targets, title=request.data.get("title", "")
                )
            else:
                if len(targets) != 1:
                    return Response(
                        {"detail": "A direct conversation needs exactly one recipient."},
                        status=status.HTTP_400_BAD_REQUEST,
                    )
                conv = services.start_direct(request.user, targets[0])
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(_conversation_payload(conv, request), status=status.HTTP_201_CREATED)


class _ConversationActionView(APIView):
    """Base for membership transitions that act on the caller and return the
    refreshed conversation."""

    permission_classes = [IsAuthenticated]
    # set in subclass: a services.* callable taking (user, conversation). Named
    # `transition` (not `action`) to avoid colliding with DRF's `view.action`.
    transition = None

    def post(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            self.transition(request.user, conv)
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(_conversation_payload(conv, request))


class ConversationAcceptView(_ConversationActionView):
    transition = staticmethod(services.accept_invite)


class ConversationDeclineView(_ConversationActionView):
    transition = staticmethod(services.decline_invite)


class ConversationLeaveView(_ConversationActionView):
    transition = staticmethod(services.leave)


class ConversationParticipantsView(APIView):
    """Add (POST) or remove (DELETE) a group member by username — admin only."""

    permission_classes = [IsAuthenticated]

    def _target(self, request):
        username = request.data.get("username") or request.query_params.get("username", "")
        return get_object_or_404(User, username=username)

    def post(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            services.add_participant(request.user, conv, self._target(request))
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(_conversation_payload(conv, request), status=status.HTTP_201_CREATED)

    def delete(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            services.remove_participant(request.user, conv, self._target(request))
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(status=status.HTTP_204_NO_CONTENT)


class ConversationMessagesView(APIView):
    """Read history (GET, with the caller's wrapped keys) or send ciphertext (POST)."""

    permission_classes = [IsAuthenticated]

    def get(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        after = request.query_params.get("after")
        try:
            after_id = int(after) if after else None
        except ValueError:
            after_id = None
        try:
            msgs = services.messages_for(request.user, conv, after_id=after_id)
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_403_FORBIDDEN)
        return Response(MessageSerializer(msgs, many=True, context={"request": request}).data)

    def post(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            msg = services.post_message(
                request.user,
                conv,
                ciphertext=request.data.get("ciphertext", ""),
                iv=request.data.get("iv", ""),
                recipient_keys=request.data.get("recipient_keys") or [],
                algorithm=request.data.get("algorithm", "AES-GCM-256"),
            )
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        msg.my_key = next((k for k in msg.keys.all() if k.recipient_id == request.user.id), None)
        return Response(
            MessageSerializer(msg, context={"request": request}).data,
            status=status.HTTP_201_CREATED,
        )


class MessageReportView(APIView):
    """Report-with-decryption: attach the plaintext the reporter can see so a
    moderator can act, even though the server can't read the ciphertext."""

    permission_classes = [IsAuthenticated]

    def post(self, request, pk, message_id):
        conv = get_object_or_404(Conversation, pk=pk)
        msg = get_object_or_404(Message, pk=message_id, conversation=conv)
        try:
            services.report_message(
                request.user,
                msg,
                reason=request.data.get("reason", "other"),
                detail=request.data.get("detail", ""),
                decrypted_excerpt=request.data.get("decrypted_excerpt", ""),
            )
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response({"detail": "Report submitted."}, status=status.HTTP_201_CREATED)
