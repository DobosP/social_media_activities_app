from rest_framework.routers import SimpleRouter

from .views import ActivityViewSet, MembershipViewSet

router = SimpleRouter()
router.register("activities", ActivityViewSet, basename="activity")
router.register("memberships", MembershipViewSet, basename="membership")

urlpatterns = router.urls
