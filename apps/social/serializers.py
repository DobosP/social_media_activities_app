from rest_framework import serializers

from apps.places.models import Place
from apps.taxonomy.models import ActivityType

from .models import Activity, Membership, Post
from .services import current_members


class ActivitySerializer(serializers.ModelSerializer):
    owner = serializers.CharField(source="owner.display_name", read_only=True)
    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    member_count = serializers.SerializerMethodField()

    class Meta:
        model = Activity
        fields = [
            "id",
            "title",
            "description",
            "owner",
            "place",
            "activity_type",
            "cohort",
            "starts_at",
            "ends_at",
            "join_threshold",
            "owner_can_override",
            "capacity",
            "status",
            "guardian_accompanied",
            "member_count",
            "created_at",
        ]
        read_only_fields = fields

    def get_member_count(self, obj) -> int:
        return current_members(obj).count()


class ActivityCreateSerializer(serializers.Serializer):
    place = serializers.PrimaryKeyRelatedField(queryset=Place.objects.all())
    activity_type = serializers.PrimaryKeyRelatedField(queryset=ActivityType.objects.all())
    title = serializers.CharField(max_length=200)
    description = serializers.CharField(required=False, allow_blank=True, default="")
    starts_at = serializers.DateTimeField()
    ends_at = serializers.DateTimeField(required=False, allow_null=True)
    join_threshold = serializers.FloatField(required=False, min_value=0.01, max_value=1.0)
    capacity = serializers.IntegerField(required=False, allow_null=True, min_value=1)
    guardian_accompanied = serializers.BooleanField(required=False, default=False)


class MembershipSerializer(serializers.ModelSerializer):
    user = serializers.CharField(source="user.display_name", read_only=True)

    class Meta:
        model = Membership
        fields = ["id", "activity", "user", "role", "state", "created_at", "decided_at"]
        read_only_fields = fields


class PostSerializer(serializers.ModelSerializer):
    author = serializers.CharField(source="author.display_name", read_only=True)

    class Meta:
        model = Post
        fields = ["id", "author", "body", "created_at"]
        read_only_fields = ["id", "author", "created_at"]
