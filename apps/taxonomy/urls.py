from rest_framework.routers import DefaultRouter

from .views import ActivityCategoryViewSet, ActivityTypeViewSet

router = DefaultRouter()
router.register("categories", ActivityCategoryViewSet, basename="category")
router.register("activities", ActivityTypeViewSet, basename="activitytype")

urlpatterns = router.urls
