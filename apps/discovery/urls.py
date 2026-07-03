from django.urls import path

from .views import (
    ActivitiesFeedView,
    ActivityDeckView,
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
    path("activity-deck/", ActivityDeckView.as_view(), name="discovery-activity-deck"),
    path("feed/", HomeFeedView.as_view(), name="discovery-feed"),
    path("public/activities/", PublicActivitiesView.as_view(), name="discovery-public-activities"),
    path("public/groups/", PublicGroupsView.as_view(), name="discovery-public-groups"),
]
