from rest_framework import serializers

from .models import Conversation, Message, Participant, PublicKey


class UserRefSerializer(serializers.Serializer):
    public_id = serializers.UUIDField(read_only=True)
    username = serializers.CharField(read_only=True)
    display_name = serializers.CharField(read_only=True)
    # The same generated avatar the rest of the app shows: the user's interest *constellation*
    # (identicon fallback when they have no interests). Deterministic — no upload, no round-trip —
    # so the chat matches the web connections/profile surfaces for the same person.
    avatar = serializers.SerializerMethodField()

    def get_avatar(self, obj):
        # Reads interests prefetched by attach_interest_nodes when the view batched them (nested
        # participant/sender lists), so a conversation/message list doesn't N+1.
        from apps.recommendations.services import interest_avatar_data_uri

        return interest_avatar_data_uri(obj)


class PublicKeySerializer(serializers.ModelSerializer):
    """Another user's key — public material only."""

    user = UserRefSerializer(read_only=True)

    class Meta:
        model = PublicKey
        fields = ["key_id", "algorithm", "public_jwk", "user", "created_at"]
        read_only_fields = fields


class OwnPublicKeySerializer(serializers.ModelSerializer):
    """The caller's own key, including the opaque private-key backup blob (only ever
    returned to its owner so they can restore it on another device)."""

    class Meta:
        model = PublicKey
        fields = ["key_id", "algorithm", "public_jwk", "wrapped_private_jwk", "created_at"]
        read_only_fields = fields


class ParticipantSerializer(serializers.ModelSerializer):
    user = UserRefSerializer(read_only=True)

    class Meta:
        model = Participant
        fields = ["user", "state", "role", "joined_at"]
        read_only_fields = fields


class ConversationSerializer(serializers.ModelSerializer):
    participants = serializers.SerializerMethodField()
    my_state = serializers.SerializerMethodField()
    my_role = serializers.SerializerMethodField()

    class Meta:
        model = Conversation
        fields = [
            "id",
            "kind",
            "title",
            "cohort",
            "disappearing_seconds",
            "created_at",
            "updated_at",
            "participants",
            "my_state",
            "my_role",
        ]
        read_only_fields = fields

    def _visible_participants(self, obj):
        return [
            p
            for p in obj.participants.all()
            if p.state in (Participant.State.ACTIVE, Participant.State.INVITED)
        ]

    def get_participants(self, obj):
        return ParticipantSerializer(self._visible_participants(obj), many=True).data

    def _mine(self, obj):
        user = self.context["request"].user
        for p in obj.participants.all():
            if p.user_id == user.id:
                return p
        return None

    def get_my_state(self, obj):
        p = self._mine(obj)
        return p.state if p else None

    def get_my_role(self, obj):
        p = self._mine(obj)
        return p.role if p else None


class MessageSerializer(serializers.ModelSerializer):
    """A message for the requesting user: ciphertext plus the key wrapped to THEM."""

    sender = UserRefSerializer(read_only=True)
    key = serializers.SerializerMethodField()

    class Meta:
        model = Message
        fields = [
            "id",
            "conversation",
            "sender",
            "algorithm",
            "ciphertext",
            "iv",
            "created_at",
            "key",
        ]
        read_only_fields = fields

    def get_key(self, obj):
        mk = getattr(obj, "my_key", None)
        if mk is None:
            return None
        return {
            "ephemeral_public_jwk": mk.ephemeral_public_jwk,
            "wrapped_key": mk.wrapped_key,
            "wrap_iv": mk.wrap_iv,
        }


def broadcast_payload(message) -> dict:
    """WebSocket fan-out payload: ALL recipients' wrapped keys are included; each
    client decrypts only the one addressed to it. The wrapped keys are individually
    encrypted, so broadcasting the full set leaks nothing."""
    return {
        "id": message.id,
        "conversation": message.conversation_id,
        "sender": UserRefSerializer(message.sender).data if message.sender else None,
        "algorithm": message.algorithm,
        "ciphertext": message.ciphertext,
        "iv": message.iv,
        "created_at": message.created_at.isoformat(),
        "keys": [
            {
                "recipient_public_id": str(k.recipient.public_id),
                "ephemeral_public_jwk": k.ephemeral_public_jwk,
                "wrapped_key": k.wrapped_key,
                "wrap_iv": k.wrap_iv,
            }
            for k in message.keys.select_related("recipient").all()
        ],
    }
