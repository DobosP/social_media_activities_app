from rest_framework.routers import SimpleRouter

from .views import SavedSearchViewSet

router = SimpleRouter()
router.register("saved-searches", SavedSearchViewSet, basename="saved-search")

urlpatterns = router.urls
