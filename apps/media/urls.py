from django.urls import path

from .views import MediaServeView, ProfilePictureView, ThreadPhotosView

urlpatterns = [
    path("profile-picture/", ProfilePictureView.as_view(), name="profile-picture"),
    path("threads/<int:thread_id>/photos/", ThreadPhotosView.as_view(), name="thread-photos"),
    path("serve/<path:key>", MediaServeView.as_view(), name="media-serve"),
]
