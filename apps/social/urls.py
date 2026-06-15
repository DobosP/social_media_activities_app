from django.urls import path
from rest_framework.routers import SimpleRouter

from .views import (
    ActivityViewSet,
    GaugeViewSet,
    GroupViewSet,
    MembershipViewSet,
    OrganizerConsoleView,
    PlaceProposalViewSet,
    SeriesViewSet,
)

router = SimpleRouter()
router.register("activities", ActivityViewSet, basename="activity")
router.register("memberships", MembershipViewSet, basename="membership")
router.register("groups", GroupViewSet, basename="group")
router.register("series", SeriesViewSet, basename="series")
router.register("gauges", GaugeViewSet, basename="gauge")
router.register("place-proposals", PlaceProposalViewSet, basename="place-proposal")

urlpatterns = [
    path("organizer-console/", OrganizerConsoleView.as_view(), name="organizer-console"),
    *router.urls,
]
