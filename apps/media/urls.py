from django.urls import path

from .views import MediaFileView, PhotoDetailView, PhotoUploadView

urlpatterns = [
    path("photos/", PhotoUploadView.as_view(), name="media-upload"),
    path("photos/<int:pk>/", PhotoDetailView.as_view(), name="media-detail"),
    path("file/<str:token>/", MediaFileView.as_view(), name="media-file"),
]
