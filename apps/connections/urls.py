from django.urls import path
from rest_framework.routers import DefaultRouter

from .views import ConnectionViewSet, PersonProfileView

router = DefaultRouter()
router.register("connections", ConnectionViewSet, basename="connection")

urlpatterns = [
    # ADR-0028: tier-gated person card (veto == 404).
    path("people/<uuid:public_id>/", PersonProfileView.as_view(), name="connection-person"),
    *router.urls,
]
