from django.conf import settings
from django.contrib.auth import get_user_model
from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.exceptions import NotFound, PermissionDenied, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response

from apps.accounts.services import is_guardian_of

from .models import Membership
from .serializers import (
    ActivityCreateSerializer,
    ActivitySerializer,
    ActivityUpdateSerializer,
    GroupCreateSerializer,
    GroupSerializer,
    MembershipSerializer,
    PostSerializer,
)
from .services import (
    InvalidState,
    NotAMember,
    NotEligible,
    SocialError,
    add_guardian,
    attendance_summary,
    can_read_thread,
    cancel_activity,
    cast_vote,
    create_activity,
    create_group,
    group_by_id,
    group_feed_activities,
    group_roster,
    join_group,
    leave_activity,
    leave_group,
    mark_arrived,
    owner_admit,
    post_to_thread,
    request_to_join,
    set_attendance_intent,
    update_activity,
    visible_activities,
    visible_groups,
    with_counts,
)


def resolve_actor(request):
    """Return the user the request acts as: the authenticated user, or — when a guardian
    passes `on_behalf_of=<ward public_id>` — the ward, after verifying guardianship."""
    public_id = request.data.get("on_behalf_of") or request.query_params.get("on_behalf_of")
    if not public_id:
        return request.user
    ward = get_user_model().objects.filter(public_id=public_id).first()
    if ward is None:
        raise ValidationError({"on_behalf_of": "No such user."})
    if not is_guardian_of(request.user, ward):
        raise PermissionDenied("You are not this user's guardian.")
    return ward


class ActivityViewSet(viewsets.ReadOnlyModelViewSet):
    """Cohort-scoped activities. Listing and retrieval only ever return activities
    in the requester's own cohort, enforcing age-cohort isolation."""

    permission_classes = [IsAuthenticated]

    def get_serializer_class(self):
        if self.action == "create":
            return ActivityCreateSerializer
        return ActivitySerializer

    def get_queryset(self):
        return with_counts(
            visible_activities(self.request.user).select_related(
                "owner", "place", "activity_type", "thread"
            )
        )

    def _actor(self, request):
        """Resolve who the action is performed as. A guardian may act on behalf of a
        ward via `on_behalf_of=<ward public_id>` — managing the child's participation."""
        return resolve_actor(request)

    def _activity_for(self, actor, pk):
        activity = visible_activities(actor).filter(pk=pk).first()
        if activity is None:
            raise NotFound("No such activity.")
        return activity

    def create(self, request):
        serializer = ActivityCreateSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        actor = self._actor(request)
        try:
            activity = create_activity(actor, **serializer.validated_data)
        except NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(ActivitySerializer(activity).data, status=status.HTTP_201_CREATED)

    def partial_update(self, request, pk=None):
        """Owner edits an OPEN, not-yet-started activity (PATCH). place/activity_type/
        cohort are not editable — see services.update_activity."""
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        serializer = ActivityUpdateSerializer(data=request.data, partial=True)
        serializer.is_valid(raise_exception=True)
        try:
            update_activity(actor, activity, **serializer.validated_data)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(ActivitySerializer(activity).data)

    @action(detail=True, methods=["post"])
    def cancel(self, request, pk=None):
        """Owner cancels the meetup; current members are notified with the reason."""
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        try:
            cancel_activity(actor, activity, reason=request.data.get("reason", ""))
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(ActivitySerializer(activity).data)

    @action(detail=True, methods=["post"])
    def rsvp(self, request, pk=None):
        """A member flips their transient go/no-go (F20). Returns the live going/total
        count for the activity. Member-only; never aggregated into per-user history."""
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        try:
            set_attendance_intent(actor, activity, request.data.get("intent"))
        except NotAMember as exc:
            raise PermissionDenied(str(exc)) from exc
        except InvalidState as exc:
            raise ValidationError(str(exc)) from exc
        return Response(attendance_summary(activity))

    @action(detail=True, methods=["post"])
    def arrived(self, request, pk=None):
        """Self-declared "I've arrived" (F3). Always the authenticated user — never
        on_behalf_of, since an arrival ping must be the member's own tap."""
        activity = self._activity_for(request.user, pk)
        try:
            membership = mark_arrived(request.user, activity)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data)

    @action(detail=True, methods=["post"])
    def join(self, request, pk=None):
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        try:
            membership = request_to_join(actor, activity)
        except NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data, status=status.HTTP_201_CREATED)

    @action(detail=True, methods=["post"])
    def leave(self, request, pk=None):
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        try:
            membership = leave_activity(actor, activity)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        if membership is None:
            raise ValidationError("You are not a member of this activity.")
        return Response(MembershipSerializer(membership).data)

    @action(detail=True, methods=["post"])
    def guardians(self, request, pk=None):
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        guardian = get_user_model().objects.filter(pk=request.data.get("user_id")).first()
        if guardian is None:
            raise ValidationError({"user_id": "No such user."})
        try:
            membership = add_guardian(actor, activity, guardian)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data, status=status.HTTP_201_CREATED)

    @action(detail=False, methods=["get"])
    def mine(self, request):
        actor = self._actor(request)
        # Hard-cap so a user with a very large membership history cannot force an
        # unbounded queryset to be materialized and serialized.
        limit = getattr(settings, "SOCIAL_MEMBERSHIP_LIST_LIMIT", 100)
        memberships = (
            Membership.objects.filter(user=actor)
            .exclude(state=Membership.State.REMOVED)
            .select_related("activity")
            .order_by("-created_at")[:limit]
        )
        return Response(MembershipSerializer(memberships, many=True).data)

    @action(detail=True, methods=["get", "post"])
    def posts(self, request, pk=None):
        actor = self._actor(request)
        activity = self._activity_for(actor, pk)
        if request.method == "POST":
            serializer = PostSerializer(data=request.data)
            serializer.is_valid(raise_exception=True)
            try:
                # A thread message is a first-person utterance — ALWAYS the authenticated
                # user, never on_behalf_of (mirrors `arrived`), so a guardian cannot ghostwrite
                # a post as a ward (and a guardian's own post is rejected by the role gate).
                post = post_to_thread(
                    request.user,
                    activity,
                    serializer.validated_data["body"],
                    reply_to=serializer.validated_data.get("reply_to"),
                    ping=serializer.validated_data.get("ping", False),
                )
            except (NotAMember, NotEligible) as exc:
                # Membership / participation failures are authorization problems (403).
                raise PermissionDenied(str(exc)) from exc
            except InvalidState as exc:
                # Cancelled/hidden/blocked/rate-limited/policy-rejected are bad-request (400).
                raise ValidationError(str(exc)) from exc
            return Response(PostSerializer(post).data, status=status.HTTP_201_CREATED)
        # GET: enforce the SINGLE read gate (membership + cohort + consent + block) before
        # serializing — `_activity_for` only checks cohort-visibility, so without this a
        # same-cohort NON-member could read a private activity thread (cohort-isolation leak).
        if not can_read_thread(actor, activity):
            raise PermissionDenied("This activity's thread is private to its members.")
        # Hard-cap the thread read to the newest N (chronological), excluding hidden posts,
        # so a long thread can't force an unbounded queryset/serialization.
        limit = getattr(settings, "SOCIAL_THREAD_POST_LIMIT", 100)
        posts = list(
            activity.thread.posts.filter(is_hidden=False)
            .select_related("author")
            .order_by("-created_at")[:limit]
        )
        posts.reverse()
        return Response(PostSerializer(posts, many=True).data)


class GroupViewSet(viewsets.ViewSet):
    """Persistent, cohort-pinned standing groups. Authenticated-only (NEVER AllowAny). EVERY read
    routes through services.visible_groups(request.user) / group_by_id — there is deliberately NO
    class-level ``queryset = Group.objects.all()``, so a cross-cohort group can never be retrieved
    by id-guessing (the classic under-gated DRF read path). No serialized member count/roster
    anywhere: the roster action returns a member LIST only, and only to an eligible ADULT member."""

    permission_classes = [IsAuthenticated]

    def list(self, request):
        return Response(GroupSerializer(visible_groups(request.user), many=True).data)

    def retrieve(self, request, pk=None):
        group = group_by_id(pk, request.user)
        if group is None:
            raise NotFound("No such group.")
        return Response(GroupSerializer(group).data)

    def create(self, request):
        serializer = GroupCreateSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        data = serializer.validated_data
        from apps.communities.services import _ensure_city_area

        area = _ensure_city_area(data["city"])
        try:
            group = create_group(
                request.user,
                area=area,
                title=data["title"],
                activity_type=data.get("activity_type"),
                description=data.get("description", ""),
                cohort=data.get("cohort") or None,
            )
        except NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        except SocialError as exc:
            raise ValidationError(str(exc)) from exc
        return Response(GroupSerializer(group).data, status=status.HTTP_201_CREATED)

    @action(detail=True, methods=["post"])
    def join(self, request, pk=None):
        try:
            join_group(request.user, pk)
        except (NotAMember, NotEligible) as exc:
            raise PermissionDenied(str(exc)) from exc
        except InvalidState as exc:
            raise ValidationError(str(exc)) from exc
        group = group_by_id(pk, request.user)
        return Response(GroupSerializer(group).data if group else {})

    @action(detail=True, methods=["post"])
    def leave(self, request, pk=None):
        try:
            membership = leave_group(request.user, pk)
        except InvalidState as exc:
            raise ValidationError(str(exc)) from exc
        if membership is None:
            raise ValidationError("You are not a member of this group.")
        return Response({"left": True})

    @action(detail=True, methods=["get"])
    def roster(self, request, pk=None):
        """The member list — ONLY for an eligible ADULT member (group_roster gates it). Minors and
        non-members get a 403, never a count. Returns names only (no scalar count field)."""
        group = group_by_id(pk, request.user)
        if group is None:
            raise NotFound("No such group.")
        roster = group_roster(group, request.user)
        if roster is None:
            raise PermissionDenied("This group's roster is not available to you.")
        return Response({"members": [u.display_name or u.username for u in roster]})

    @action(detail=True, methods=["get"])
    def activities(self, request, pk=None):
        group = group_by_id(pk, request.user)
        if group is None:
            raise NotFound("No such group.")
        from apps.discovery.serializers import ActivityCardSerializer

        limit = getattr(settings, "COMMUNITY_ACTIVITIES_PAGE_SIZE", 100)
        acts = group_feed_activities(group, request.user)[:limit]
        return Response(ActivityCardSerializer(acts, many=True).data)


class MembershipViewSet(viewsets.ReadOnlyModelViewSet):
    """Join requests / memberships for activities in the requester's cohort."""

    permission_classes = [IsAuthenticated]
    serializer_class = MembershipSerializer

    def get_queryset(self):
        return Membership.objects.filter(
            activity__in=visible_activities(self.request.user)
        ).select_related("user", "activity")

    @action(detail=True, methods=["post"])
    def vote(self, request, pk=None):
        membership = self.get_object()
        approve = request.data.get("approve")
        if not isinstance(approve, bool):
            raise ValidationError({"approve": "A boolean 'approve' value is required."})
        try:
            membership = cast_vote(request.user, membership, approve)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data)

    @action(detail=True, methods=["post"])
    def admit(self, request, pk=None):
        membership = self.get_object()
        try:
            membership = owner_admit(request.user, membership)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data)
