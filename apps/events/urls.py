from rest_framework.routers import SimpleRouter

from .views import EventViewSet

router = SimpleRouter()
router.register("", EventViewSet, basename="event")

urlpatterns = router.urls
