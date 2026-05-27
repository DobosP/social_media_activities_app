from django.urls import path
from rest_framework.routers import SimpleRouter

from .views import BookingOptionsView, BookingProvidersView, BookingViewSet

router = SimpleRouter()
router.register("bookings", BookingViewSet, basename="booking")

urlpatterns = [
    path("options/", BookingOptionsView.as_view(), name="booking-options"),
    path("providers/", BookingProvidersView.as_view(), name="booking-providers"),
    *router.urls,
]
