from django.urls import path

from .views import (
    ActivitiesFeedView,
    HappeningView,
    HomeFeedView,
    NearMeView,
    PublicActivitiesView,
    PublicGroupsView,
)

urlpatterns = [
    path("near-me/", NearMeView.as_view(), name="discovery-near-me"),
    path("happening/", HappeningView.as_view(), name="discovery-happening"),
    path("activities/", ActivitiesFeedView.as_view(), name="discovery-activities"),
    path("feed/", HomeFeedView.as_view(), name="discovery-feed"),
    path("public/activities/", PublicActivitiesView.as_view(), name="discovery-public-activities"),
    path("public/groups/", PublicGroupsView.as_view(), name="discovery-public-groups"),
]
