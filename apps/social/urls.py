from rest_framework.routers import SimpleRouter

from .views import ActivityViewSet, GroupViewSet, MembershipViewSet

router = SimpleRouter()
router.register("activities", ActivityViewSet, basename="activity")
router.register("memberships", MembershipViewSet, basename="membership")
router.register("groups", GroupViewSet, basename="group")

urlpatterns = router.urls
