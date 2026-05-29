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
        "guardian/conversations/",
        views.GuardianConversationsView.as_view(),
        name="messaging-guardian-conversations",
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
    path(
        "conversations/<int:pk>/disappearing/",
        views.ConversationDisappearingView.as_view(),
        name="messaging-disappearing",
    ),
    path(
        "conversations/<int:pk>/keys/",
        views.ConversationKeysView.as_view(),
        name="messaging-conversation-keys",
    ),
    path(
        "conversations/<int:pk>/guardian/",
        views.ConversationGuardianView.as_view(),
        name="messaging-guardian",
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
