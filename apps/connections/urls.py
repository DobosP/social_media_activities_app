from rest_framework.routers import DefaultRouter

from .views import ConnectionViewSet

router = DefaultRouter()
router.register("connections", ConnectionViewSet, basename="connection")

urlpatterns = router.urls
