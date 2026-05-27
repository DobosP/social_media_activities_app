from django.urls import path

from .views import MeView, WardDetailView, WardListView

urlpatterns = [
    path("me/", MeView.as_view(), name="me"),
    path("wards/", WardListView.as_view(), name="wards"),
    path("wards/<uuid:public_id>/", WardDetailView.as_view(), name="ward-detail"),
]
