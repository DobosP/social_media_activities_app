from rest_framework import viewsets
from rest_framework.decorators import action
from rest_framework.exceptions import NotFound
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response

# A LEAN, count-free activity card (no member_count/open_positions) — the community surface must
# never expose a per-activity count that a client could sum into a community-level aggregate.
from apps.discovery.serializers import ActivityCardSerializer

from . import services
from .serializers import CommunitySerializer


class CommunityViewSet(viewsets.ViewSet):
    """Read-only community discovery, cohort-walled. Authenticated-only (never AllowAny): a
    viewer only ever sees published communities of their OWN cohort, and a community's activities
    are the existing cohort-filtered feed narrowed by the community predicate. No roster, no
    counts, no contact affordance."""

    permission_classes = [IsAuthenticated]
    lookup_field = "slug"

    def list(self, request):
        qs = services.visible_communities(request.user)
        return Response(CommunitySerializer(qs, many=True).data)

    def retrieve(self, request, slug=None):
        community = services.community_by_slug(slug, request.user)
        if community is None:
            raise NotFound("No such community.")
        return Response(CommunitySerializer(community).data)

    @action(detail=True, methods=["get"])
    def activities(self, request, slug=None):
        community = services.community_by_slug(slug, request.user)
        if community is None:
            raise NotFound("No such community.")
        # Hard cap so a community can't dump an unbounded list.
        from django.conf import settings

        limit = getattr(settings, "COMMUNITY_ACTIVITIES_PAGE_SIZE", 100)
        acts = services.community_activities(community, request.user)[:limit]
        return Response(ActivityCardSerializer(acts, many=True).data)
