from django.urls import path

from .views import (
    ActivityCoverFileView,
    ActivityCoverView,
    AttachmentFileView,
    MediaFileView,
    PhotoDetailView,
    PhotoUploadView,
    PlaceCoverFileView,
    ThreadPhotosView,
)

urlpatterns = [
    path("photos/", PhotoUploadView.as_view(), name="media-upload"),
    path("photos/<int:pk>/", PhotoDetailView.as_view(), name="media-detail"),
    path("threads/<int:thread_id>/photos/", ThreadPhotosView.as_view(), name="media-thread"),
    path(
        "activity-covers/<int:activity_id>/",
        ActivityCoverView.as_view(),
        name="media-activity-cover",
    ),
    path("file/<str:token>/", MediaFileView.as_view(), name="media-file"),
    path("attachment/<str:token>/", AttachmentFileView.as_view(), name="media-attachment"),
    path(
        "activity-cover-file/<str:token>/",
        ActivityCoverFileView.as_view(),
        name="media-activity-cover-file",
    ),
    path(
        "place-cover-file/<str:token>/",
        PlaceCoverFileView.as_view(),
        name="media-place-cover-file",
    ),
]
