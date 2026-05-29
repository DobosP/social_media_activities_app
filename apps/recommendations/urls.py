from django.urls import path

from .views import InterestOptionsView, InterestsView, RecommendationsView

urlpatterns = [
    path("interests/", InterestsView.as_view(), name="rec-interests"),
    path("interests/options/", InterestOptionsView.as_view(), name="rec-interest-options"),
    path("activities/", RecommendationsView.as_view(), name="rec-activities"),
]
