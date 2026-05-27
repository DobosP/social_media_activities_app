from rest_framework import status, viewsets
from rest_framework.decorators import action
from rest_framework.exceptions import PermissionDenied, ValidationError
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response

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

    def create(self, request):
        serializer = ActivityCreateSerializer(data=request.data)
        serializer.is_valid(raise_exception=True)
        try:
            activity = create_activity(request.user, **serializer.validated_data)
        except NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(ActivitySerializer(activity).data, status=status.HTTP_201_CREATED)

    @action(detail=True, methods=["post"])
    def join(self, request, pk=None):
        activity = self.get_object()
        try:
            membership = request_to_join(request.user, activity)
        except NotEligible as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data, status=status.HTTP_201_CREATED)

    @action(detail=True, methods=["post"])
    def leave(self, request, pk=None):
        activity = self.get_object()
        try:
            membership = leave_activity(request.user, activity)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        if membership is None:
            raise ValidationError("You are not a member of this activity.")
        return Response(MembershipSerializer(membership).data)

    @action(detail=True, methods=["post"])
    def guardians(self, request, pk=None):
        activity = self.get_object()
        from django.contrib.auth import get_user_model

        guardian = get_user_model().objects.filter(pk=request.data.get("user_id")).first()
        if guardian is None:
            raise ValidationError({"user_id": "No such user."})
        try:
            membership = add_guardian(request.user, activity, guardian)
        except SocialError as exc:
            raise PermissionDenied(str(exc)) from exc
        return Response(MembershipSerializer(membership).data, status=status.HTTP_201_CREATED)

    @action(detail=False, methods=["get"])
    def mine(self, request):
        memberships = (
            Membership.objects.filter(user=request.user)
            .exclude(state=Membership.State.REMOVED)
            .select_related("activity")
        )
        return Response(MembershipSerializer(memberships, many=True).data)

    @action(detail=True, methods=["get", "post"])
    def posts(self, request, pk=None):
        activity = self.get_object()
        if request.method == "POST":
            serializer = PostSerializer(data=request.data)
            serializer.is_valid(raise_exception=True)
            try:
                post = post_to_thread(request.user, activity, serializer.validated_data["body"])
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
