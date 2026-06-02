from rest_framework import serializers

from apps.places.models import Place
from apps.taxonomy.models import ActivityType

from .models import Activity, Membership, Post
from .services import participant_count

# Hard text caps enforced at the serialization layer. The underlying model fields
# are unbounded TextFields, so without these the API would accept arbitrarily large
# bodies/descriptions (abuse + storage/perf risk). Posts mirror the E2EE chat cap.
POST_BODY_MAX_LENGTH = 4000
ACTIVITY_DESCRIPTION_MAX_LENGTH = 2000
# F9 logistics fields (meeting point / what to bring / organiser note). Capped at the API
# edge on BOTH the create and update serializers (the model TextFields stay unbounded, like
# description). The form enforces the same cap for the web path.
LOGISTICS_FIELD_MAX_LENGTH = 500


class ActivitySerializer(serializers.ModelSerializer):
    owner = serializers.CharField(source="owner.display_name", read_only=True)
    activity_type = serializers.SlugRelatedField(slug_field="slug", read_only=True)
    # NO member_count: a raw "N members/going" number on the discovery feed is exactly the kind of
    # social-proof vanity metric inv.2 forbids, and it leaked unconditionally to every cohort
    # (including minors). It is removed outright (not merely cohort-gated). open_positions stays —
    # that is FUNCTIONAL capacity info ("3 spots left"), not social proof.
    open_positions = serializers.SerializerMethodField()

    class Meta:
        model = Activity
        fields = [
            "id",
            "title",
            "description",
            "meeting_point",
            "what_to_bring",
            "organizer_note",
            "cost_band",
            "difficulty",
            "accessibility_notes",
            "beginners_welcome",
            "owner",
            "place",
            "activity_type",
            "cohort",
            "starts_at",
            "ends_at",
            "join_threshold",
            "owner_can_override",
            "capacity",
            "min_to_go",
            "status",
            "guardian_accompanied",
            "open_positions",
            "created_at",
        ]
        read_only_fields = fields

    def get_open_positions(self, obj) -> int | None:
        if obj.capacity is None:
            return None
        annotated = getattr(obj, "participant_n", None)
        taken = annotated if annotated is not None else participant_count(obj)
        return max(obj.capacity - taken, 0)


class ActivityCreateSerializer(serializers.Serializer):
    place = serializers.PrimaryKeyRelatedField(queryset=Place.objects.all())
    activity_type = serializers.PrimaryKeyRelatedField(queryset=ActivityType.objects.all())
    title = serializers.CharField(max_length=200)
    # The model stores description as an unbounded TextField; cap it here so the
    # API rejects overlong input instead of persisting arbitrarily large blobs.
    description = serializers.CharField(
        required=False, allow_blank=True, default="", max_length=ACTIVITY_DESCRIPTION_MAX_LENGTH
    )
    starts_at = serializers.DateTimeField()
    ends_at = serializers.DateTimeField(required=False, allow_null=True)
    join_threshold = serializers.FloatField(required=False, min_value=0.01, max_value=1.0)
    capacity = serializers.IntegerField(required=False, allow_null=True, min_value=1)
    min_to_go = serializers.IntegerField(required=False, allow_null=True, min_value=1)
    guardian_accompanied = serializers.BooleanField(required=False, default=False)
    meeting_point = serializers.CharField(
        required=False, allow_blank=True, default="", max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    what_to_bring = serializers.CharField(
        required=False, allow_blank=True, default="", max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    organizer_note = serializers.CharField(
        required=False, allow_blank=True, default="", max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    cost_band = serializers.ChoiceField(
        choices=Activity.CostBand.choices, required=False, default=Activity.CostBand.UNSPECIFIED
    )
    difficulty = serializers.ChoiceField(
        choices=Activity.Difficulty.choices, required=False, default=Activity.Difficulty.UNSPECIFIED
    )
    accessibility_notes = serializers.CharField(
        required=False, allow_blank=True, default="", max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    beginners_welcome = serializers.BooleanField(required=False, default=False)


class ActivityUpdateSerializer(serializers.Serializer):
    """Partial edit of an OPEN, not-yet-started activity. Only the owner-editable fields
    are accepted; place/activity_type/cohort/guardian_accompanied are intentionally absent
    so an edit can never change the meetup's identity or cohort pin (see
    services.ACTIVITY_EDITABLE_FIELDS)."""

    title = serializers.CharField(required=False, max_length=200)
    description = serializers.CharField(
        required=False, allow_blank=True, max_length=ACTIVITY_DESCRIPTION_MAX_LENGTH
    )
    starts_at = serializers.DateTimeField(required=False)
    ends_at = serializers.DateTimeField(required=False, allow_null=True)
    capacity = serializers.IntegerField(required=False, allow_null=True, min_value=1)
    min_to_go = serializers.IntegerField(required=False, allow_null=True, min_value=1)
    meeting_point = serializers.CharField(
        required=False, allow_blank=True, max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    what_to_bring = serializers.CharField(
        required=False, allow_blank=True, max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    organizer_note = serializers.CharField(
        required=False, allow_blank=True, max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    # No defaults below: a default would inject the field on every partial PATCH that omits
    # it and silently overwrite the stored value (e.g. reset beginners_welcome to False).
    cost_band = serializers.ChoiceField(choices=Activity.CostBand.choices, required=False)
    difficulty = serializers.ChoiceField(choices=Activity.Difficulty.choices, required=False)
    accessibility_notes = serializers.CharField(
        required=False, allow_blank=True, max_length=LOGISTICS_FIELD_MAX_LENGTH
    )
    beginners_welcome = serializers.BooleanField(required=False)


class MembershipSerializer(serializers.ModelSerializer):
    user = serializers.CharField(source="user.display_name", read_only=True)

    class Meta:
        model = Membership
        fields = [
            "id",
            "activity",
            "user",
            "role",
            "state",
            "attendance_intent",
            "arrived_at",
            "created_at",
            "decided_at",
        ]
        read_only_fields = fields


class GroupSerializer(serializers.Serializer):
    """A standing-group card / detail. STRICT ALLOWLIST — deliberately NO member_count / roster /
    participants / open_positions / any ``*_count`` or ``*_n`` field, for ANY cohort (the headline
    roster-less rule; a regression test asserts no such key is ever added here). The roster, when an
    ADULT member may see it, is a server-rendered list via services.group_roster — never a
    serialized scalar a client could read directly or sum into an aggregate."""

    id = serializers.IntegerField(read_only=True)
    title = serializers.CharField(read_only=True)
    description = serializers.CharField(read_only=True)
    owner = serializers.CharField(source="owner.display_name", read_only=True)
    area = serializers.CharField(source="area.name", read_only=True)
    category = serializers.CharField(source="category.name", read_only=True)
    activity_type = serializers.SerializerMethodField()
    tier = serializers.CharField(read_only=True)
    cohort = serializers.CharField(read_only=True)
    status = serializers.CharField(read_only=True)
    is_staff_curated = serializers.BooleanField(read_only=True)
    created_at = serializers.DateTimeField(read_only=True)

    def get_activity_type(self, obj):
        return obj.activity_type.name if obj.activity_type_id else None


class GroupCreateSerializer(serializers.Serializer):
    """Create a group from a city + a taxonomy coordinate (an activity_type, or a category for a
    rollup group). cohort is OPTIONAL and only honoured for staff (a staff curator creates a minor
    group); a non-staff actor's group is always pinned to their own cohort by the service."""

    city = serializers.CharField(max_length=128)
    activity_type = serializers.PrimaryKeyRelatedField(
        queryset=ActivityType.objects.all(), required=False, allow_null=True
    )
    title = serializers.CharField(max_length=200)
    description = serializers.CharField(
        required=False, allow_blank=True, default="", max_length=ACTIVITY_DESCRIPTION_MAX_LENGTH
    )
    cohort = serializers.CharField(required=False, allow_blank=True, default="")


class ProposePlaceSerializer(serializers.Serializer):
    """Propose a new venue (F25): a name + coordinate + the activity it supports. ``allow_nearby``
    lets the proposer override the soft 'something is very close' duplicate guard."""

    name = serializers.CharField(max_length=255)
    lon = serializers.FloatField(min_value=-180, max_value=180)
    lat = serializers.FloatField(min_value=-90, max_value=90)
    activity_type = serializers.PrimaryKeyRelatedField(queryset=ActivityType.objects.all())
    allow_nearby = serializers.BooleanField(required=False, default=False)


class PlaceProposalSerializer(serializers.Serializer):
    """A pending user-place proposal others may confirm. STRICT ALLOWLIST — confirmation COUNTS
    only, NEVER the proposer's or any confirmer's identity (mirrors the web 'counts only' rule and
    the `pending_proposals_for` annotation). A confirmer sees what/where the venue is, never who."""

    id = serializers.IntegerField(read_only=True)
    place_id = serializers.IntegerField(read_only=True)
    place_name = serializers.CharField(source="place.name", read_only=True)
    lon = serializers.SerializerMethodField()
    lat = serializers.SerializerMethodField()
    status = serializers.CharField(read_only=True)
    required_confirmations = serializers.IntegerField(read_only=True)
    confirmations_count = serializers.SerializerMethodField()
    created_at = serializers.DateTimeField(read_only=True)

    def get_lon(self, obj):
        return obj.place.location.x if obj.place.location else None

    def get_lat(self, obj):
        return obj.place.location.y if obj.place.location else None

    def get_confirmations_count(self, obj):
        # `pending_proposals_for` annotates this; fall back to a count when serialized standalone.
        annotated = getattr(obj, "confirmations_count", None)
        return annotated if annotated is not None else obj.confirmations.count()


class PostSerializer(serializers.ModelSerializer):
    author = serializers.CharField(source="author.display_name", read_only=True)
    # Explicit cap: Post.body is a TextField (unbounded), so declare the limit here
    # to reject overlong posts at the API boundary.
    body = serializers.CharField(max_length=POST_BODY_MAX_LENGTH)
    # Optional one-level quote-reply target; the service validates it (same thread, not hidden)
    # and re-parents to the top-level ancestor. Read back as the (possibly re-parented) id.
    reply_to = serializers.PrimaryKeyRelatedField(
        queryset=Post.objects.all(), required=False, allow_null=True
    )
    # Write-only opt-in: escalate @mentions in the body from a calm tag to a MENTION notification
    # to the mentioned peers (default off = tag-not-ping). Never echoed back.
    ping = serializers.BooleanField(required=False, default=False, write_only=True)

    class Meta:
        model = Post
        fields = ["id", "author", "body", "is_announcement", "reply_to", "ping", "created_at"]
        read_only_fields = ["id", "author", "is_announcement", "created_at"]
