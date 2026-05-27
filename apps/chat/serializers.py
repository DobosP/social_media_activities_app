from rest_framework import serializers

from .models import ChatMessage


class AuthorRefSerializer(serializers.Serializer):
    public_id = serializers.UUIDField(read_only=True)
    display_name = serializers.CharField(read_only=True)


class ChatMessageSerializer(serializers.ModelSerializer):
    author = AuthorRefSerializer(read_only=True)

    class Meta:
        model = ChatMessage
        fields = ["id", "thread", "author", "body", "redacted", "created_at"]
        read_only_fields = fields
