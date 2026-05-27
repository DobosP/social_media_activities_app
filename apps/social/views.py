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
    MembershipSerializer,
    PostSerializer,
)
from .services import (
    NotAMember,
    NotEligible,
    SocialError,
    add_guardian,
    cast_vote,
    create_activity,
    leave_activity,
    owner_admit,
    post_to_thread,
    request_to_join,
    visible_activities,
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
        return visible_activities(self.request.user).select_related(
            "owner", "place", "activity_type", "thread"
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
        memberships = (
            Membership.objects.filter(user=actor)
            .exclude(state=Membership.State.REMOVED)
            .select_related("activity")
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
                post = post_to_thread(actor, activity, serializer.validated_data["body"])
            except NotAMember as exc:
                raise PermissionDenied(str(exc)) from exc
            return Response(PostSerializer(post).data, status=status.HTTP_201_CREATED)
        posts = activity.thread.posts.select_related("author")
        return Response(PostSerializer(posts, many=True).data)


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
