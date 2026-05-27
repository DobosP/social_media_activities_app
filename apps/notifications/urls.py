from django.urls import path
from rest_framework.routers import SimpleRouter

from .views import NotificationViewSet, PreferenceView

router = SimpleRouter()
router.register("", NotificationViewSet, basename="notification")

urlpatterns = [
    path("preferences/", PreferenceView.as_view(), name="notification-preferences"),
    *router.urls,
]
