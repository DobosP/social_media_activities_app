from django.contrib.gis.geos import Point
from django.utils.translation import gettext as _
from rest_framework.permissions import IsAuthenticated
from rest_framework.response import Response
from rest_framework.views import APIView

from apps.social.serializers import ActivitySerializer
from apps.taxonomy.models import ActivityType

from . import services


class InterestsView(APIView):
    """Read or replace the current user's declared activity interests (by type slug)."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        slugs = list(services.get_interests(request.user).values_list("slug", flat=True))
        return Response({"interests": slugs})

    def put(self, request):
        slugs = request.data.get("interests", request.data.get("activity_types", []))
        if not isinstance(slugs, list):
            return Response({"detail": _("`interests` must be a list of type slugs.")}, status=400)
        types = services.set_interests(request.user, slugs)
        known = {t.slug for t in types}
        unknown = [s for s in slugs if s not in known]
        return Response({"interests": sorted(known), "ignored": unknown})


class TopicsView(APIView):
    """Read or replace the current user's chosen feed TOPICS (taxonomy category slugs) — the
    user's hand on the suggestion algorithm. A SOFT signal that only re-orders + labels
    cohort-visible suggestions, never hides anything."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        slugs = sorted(services.topic_preference_slugs(request.user))
        return Response({"topics": slugs})

    def put(self, request):
        slugs = request.data.get("topics", [])
        if not isinstance(slugs, list):
            return Response({"detail": _("`topics` must be a list of category slugs.")}, status=400)
        categories = services.set_topic_preferences(request.user, slugs)
        known = {c.slug for c in categories}
        unknown = [s for s in slugs if s not in known]
        return Response({"topics": sorted(known), "ignored": unknown})


class RecommendationsView(APIView):
    """Activities for you nearby: cohort-scoped, interest-ranked upcoming activities."""

    permission_classes = [IsAuthenticated]

    def get(self, request):
        params = request.query_params
        try:
            limit = min(max(int(params.get("limit", 20)), 1), 50)
        except (TypeError, ValueError):
            limit = 20

        near_point = None
        radius_m = None
        if params.get("near_lon") and params.get("near_lat"):
            try:
                near_point = Point(float(params["near_lon"]), float(params["near_lat"]), srid=4326)
                radius_m = float(params.get("radius_m", 10000))
            except (TypeError, ValueError):
                near_point = None

        activities = services.recommend_activities(
            request.user, limit=limit, near_point=near_point, radius_m=radius_m
        )
        data = ActivitySerializer(activities, many=True).data
        for item, activity in zip(data, activities, strict=False):
            distance = getattr(activity, "rec_distance", None)
            if distance is not None:
                item["match_score"] = round(1.0 - float(distance), 4)
        return Response({"results": data})


# Activity types available to pick as interests (handy for clients building the UI).
class InterestOptionsView(APIView):
    permission_classes = [IsAuthenticated]

    def get(self, request):
        options = (
            ActivityType.objects.filter(is_active=True).order_by("name").values("slug", "name")
        )
        return Response({"options": list(options)})
