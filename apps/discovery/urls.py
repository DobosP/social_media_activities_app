from django.urls import path

from .views import ActivitiesFeedView, HappeningView, NearMeView

urlpatterns = [
    path("near-me/", NearMeView.as_view(), name="discovery-near-me"),
    path("happening/", HappeningView.as_view(), name="discovery-happening"),
    path("activities/", ActivitiesFeedView.as_view(), name="discovery-activities"),
]
