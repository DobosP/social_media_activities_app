from rest_framework.routers import DefaultRouter

from .views import CommunityViewSet

router = DefaultRouter()
router.register("communities", CommunityViewSet, basename="community")

urlpatterns = router.urls
