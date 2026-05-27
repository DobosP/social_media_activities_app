from django.urls import path

from .views import MediaFileView, PhotoDetailView, PhotoUploadView, ThreadPhotosView

urlpatterns = [
    path("photos/", PhotoUploadView.as_view(), name="media-upload"),
    path("photos/<int:pk>/", PhotoDetailView.as_view(), name="media-detail"),
    path("threads/<int:thread_id>/photos/", ThreadPhotosView.as_view(), name="media-thread"),
    path("file/<str:token>/", MediaFileView.as_view(), name="media-file"),
]
