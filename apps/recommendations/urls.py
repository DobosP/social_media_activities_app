from django.urls import path

from .views import InterestOptionsView, InterestsView, RecommendationsView, TopicsView

urlpatterns = [
    path("interests/", InterestsView.as_view(), name="rec-interests"),
    path("interests/options/", InterestOptionsView.as_view(), name="rec-interest-options"),
    path("topics/", TopicsView.as_view(), name="rec-topics"),
    path("activities/", RecommendationsView.as_view(), name="rec-activities"),
]
