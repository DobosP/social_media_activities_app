from django.urls import path

from .views import ThreadMessagesView

urlpatterns = [
    path("threads/<int:thread_id>/messages/", ThreadMessagesView.as_view(), name="thread-messages"),
]
