from django.urls import path

from .views import BatchEventsView, MatchPlaceView

urlpatterns = [
    path("batch-events/", BatchEventsView.as_view(), name="ingestion-batch-events"),
    path("match-place/", MatchPlaceView.as_view(), name="ingestion-match-place"),
]
