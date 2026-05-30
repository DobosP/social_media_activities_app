from rest_framework import serializers


class UserRefSerializer(serializers.Serializer):
    """A minimal, privacy-safe public reference to a user — never exposes PII beyond the
    self-chosen display name + the opaque public_id."""

    public_id = serializers.UUIDField(read_only=True)
    display_name = serializers.SerializerMethodField()

    def get_display_name(self, obj):
        return obj.display_name or obj.username


class ConnectionSerializer(serializers.Serializer):
    id = serializers.IntegerField(read_only=True)
    status = serializers.CharField(read_only=True)
    requester = UserRefSerializer(read_only=True)
    addressee = UserRefSerializer(read_only=True)
    created_at = serializers.DateTimeField(read_only=True)
