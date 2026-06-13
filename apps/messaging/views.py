from django.conf import settings
from django.contrib.auth import get_user_model
from django.shortcuts import get_object_or_404
from rest_framework import status
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from . import services
from .models import Conversation, Message, Participant
from .serializers import (
    ConversationSerializer,
    MessageSerializer,
    OwnPublicKeySerializer,
    PublicKeySerializer,
)

User = get_user_model()


def _attach_avatars(users):
    """Batch interest nodes onto the given users so UserRefSerializer's nested constellation
    avatars render from cache, instead of one interest query per participant/sender (N+1)."""
    from apps.recommendations.services import attach_interest_nodes

    attach_interest_nodes(users)


def _conversation_payload(conv, request):
    conv = Conversation.objects.prefetch_related("participants__user").get(pk=conv.pk)
    _attach_avatars([p.user for p in conv.participants.all()])
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
        data = PublicKeySerializer(key).data
        # Fingerprint + whether the caller has verified this exact key (for the
        # safety-number UI). The browser recomputes the fingerprint to trust it.
        data.update(services.verification_status(request.user, target))
        return Response(data)


class KeyVerifyView(APIView):
    """Record that the caller verified a contact's key out of band (safety number).
    Rejects a fingerprint that doesn't match the contact's current key."""

    permission_classes = [IsAuthenticated]

    def post(self, request):
        target = get_object_or_404(User, username=request.data.get("username", ""))
        try:
            services.record_key_verification(
                request.user, target, request.data.get("fingerprint", "")
            )
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(services.verification_status(request.user, target))


class ConversationListCreateView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        # Hard-cap the list so a user with a very large number of conversations
        # cannot make the server materialize and serialize an unbounded queryset
        # (mirrors the notifications [:100] bound). `conversations_for` already
        # orders by -updated_at, so this keeps the most recently active ones.
        limit = getattr(settings, "MESSAGING_CONVERSATION_LIST_LIMIT", 100)
        qs = services.conversations_for(request.user)
        # W1 search: ?q= filters by conversation METADATA only (group title, participant
        # names) — message bodies are E2EE ciphertext the server cannot search. Name
        # matching is restricted to the participant states the serializer actually shows
        # (ACTIVE/INVITED), so someone who LEFT/was removed can't be rediscovered through
        # search on a surface that deliberately hides them (review W1-2).
        query = (request.query_params.get("q") or "").strip()
        if len(query) >= 2:
            from django.db.models import Q as _Q

            shown_states = [Participant.State.ACTIVE, Participant.State.INVITED]
            name_match = _Q(participants__state__in=shown_states) & (
                _Q(participants__user__display_name__icontains=query)
                | _Q(participants__user__username__icontains=query)
            )
            qs = qs.filter(_Q(title__icontains=query) | name_match).distinct()
        convs = list(qs.prefetch_related("participants__user")[:limit])
        _attach_avatars([p.user for c in convs for p in c.participants.all()])
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


class ConversationDisappearingView(APIView):
    """Set the disappearing-messages timer (seconds; 0 = off)."""

    permission_classes = [IsAuthenticated]

    def post(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            services.set_disappearing(request.user, conv, request.data.get("seconds", 0))
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(_conversation_payload(conv, request))


class ConversationKeysView(APIView):
    """Public keys of the conversation's active members, for the client to encrypt to.
    Scoped to members, so it also yields a (cross-cohort) guardian observer's key."""

    permission_classes = [IsAuthenticated]

    def get(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            keys = services.participant_keys(request.user, conv)
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_403_FORBIDDEN)
        return Response(keys)


class ConversationGuardianView(APIView):
    """A verified guardian enrolls themselves as a read-only observer of a conversation
    their CHILD ward is in. Transparent to all members."""

    permission_classes = [IsAuthenticated]

    def post(self, request, pk):
        conv = get_object_or_404(Conversation, pk=pk)
        try:
            services.add_guardian_observer(request.user, conv)
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_400_BAD_REQUEST)
        return Response(_conversation_payload(conv, request), status=status.HTTP_201_CREATED)


class GuardianConversationsView(APIView):
    """Discovery: conversations the caller (a guardian) may observe — those where an
    active CHILD ward is an active member."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        convs = list(
            services.guardian_observable_conversations(request.user).prefetch_related(
                "participants__user"
            )
        )
        _attach_avatars([p.user for c in convs for p in c.participants.all()])
        return Response(ConversationSerializer(convs, many=True, context={"request": request}).data)


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
        # History is always bounded to the newest-N messages so a long-lived
        # conversation can never return an unbounded page. A caller-supplied
        # `?limit=` may only shrink the window, never exceed the hard cap.
        max_limit = getattr(settings, "MESSAGING_MESSAGE_PAGE_LIMIT", 50)
        limit = max_limit
        requested = request.query_params.get("limit")
        if requested:
            try:
                limit = max(1, min(int(requested), max_limit))
            except ValueError:
                limit = max_limit
        try:
            msgs = services.messages_for(request.user, conv, limit=limit, after_id=after_id)
        except services.MessagingError as exc:
            return Response({"detail": str(exc)}, status=status.HTTP_403_FORBIDDEN)
        _attach_avatars([m.sender for m in msgs if m.sender_id])
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
