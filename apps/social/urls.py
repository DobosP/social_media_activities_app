from rest_framework.routers import SimpleRouter

from .views import ActivityViewSet, GroupViewSet, MembershipViewSet, PlaceProposalViewSet

router = SimpleRouter()
router.register("activities", ActivityViewSet, basename="activity")
router.register("memberships", MembershipViewSet, basename="membership")
router.register("groups", GroupViewSet, basename="group")
router.register("place-proposals", PlaceProposalViewSet, basename="place-proposal")

urlpatterns = router.urls
