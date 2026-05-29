from django.urls import path

from . import views

urlpatterns = [
    # Key registry & out-of-band verification
    path("keys/", views.KeyRegistryView.as_view(), name="messaging-keys"),
    path("verify/", views.KeyVerifyView.as_view(), name="messaging-verify"),
    path("keys/<str:username>/", views.UserKeyView.as_view(), name="messaging-user-key"),
    # Conversations
    path(
        "conversations/", views.ConversationListCreateView.as_view(), name="messaging-conversations"
    ),
    path(
        "conversations/<int:pk>/accept/",
        views.ConversationAcceptView.as_view(),
        name="messaging-accept",
    ),
    path(
        "conversations/<int:pk>/decline/",
        views.ConversationDeclineView.as_view(),
        name="messaging-decline",
    ),
    path(
        "conversations/<int:pk>/leave/",
        views.ConversationLeaveView.as_view(),
        name="messaging-leave",
    ),
    path(
        "conversations/<int:pk>/participants/",
        views.ConversationParticipantsView.as_view(),
        name="messaging-participants",
    ),
    # Messages
    path(
        "conversations/<int:pk>/messages/",
        views.ConversationMessagesView.as_view(),
        name="messaging-messages",
    ),
    path(
        "conversations/<int:pk>/messages/<int:message_id>/report/",
        views.MessageReportView.as_view(),
        name="messaging-report",
    ),
]
