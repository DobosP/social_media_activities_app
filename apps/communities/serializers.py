from rest_framework import serializers


class CommunitySerializer(serializers.Serializer):
    """A community card. STRICT ALLOWLIST — deliberately NO member/participant count, NO roster,
    NO threshold inputs: membership is unanswerable by design, so it can never become a vanity
    metric. (A regression test asserts no count/roster field is ever added here.)"""

    slug = serializers.CharField(read_only=True)
    name = serializers.CharField(read_only=True)
    tier = serializers.CharField(read_only=True)
    area = serializers.CharField(source="area.name", read_only=True)
    category = serializers.CharField(source="category.name", read_only=True)
    activity_type = serializers.SerializerMethodField()

    def get_activity_type(self, obj):
        return obj.activity_type.name if obj.activity_type_id else None
