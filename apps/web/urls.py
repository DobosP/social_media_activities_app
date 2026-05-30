from django.contrib.auth import views as auth_views
from django.urls import path

from . import views

urlpatterns = [
    path("", views.home, name="home"),
    # Auth
    path("register/", views.register, name="register"),
    path("login/", views.ThrottledLoginView.as_view(), name="login"),
    path("logout/", auth_views.LogoutView.as_view(), name="logout"),
    # Discover
    path("places/", views.places_map, name="places_map"),
    path("places/list/", views.places_list, name="places_list"),
    path("places/<int:pk>/", views.place_detail, name="place_detail"),
    path("events/", views.events_list, name="events_list"),
    path("events/<int:pk>/", views.event_detail, name="event_detail"),
    # Activities
    path("activities/", views.activity_list, name="activity_list"),
    path("activities/new/", views.activity_create, name="activity_create"),
    path("activities/<int:pk>/", views.activity_detail, name="activity_detail"),
    path("activities/<int:pk>/edit/", views.activity_edit, name="activity_edit"),
    path("activities/<int:pk>/cancel/", views.activity_cancel, name="activity_cancel"),
    path("activities/<int:pk>/announce/", views.activity_announce, name="activity_announce"),
    path("activities/<int:pk>/rsvp/", views.activity_rsvp, name="activity_rsvp"),
    path("activities/<int:pk>/arrived/", views.activity_arrived, name="activity_arrived"),
    path("activities/<int:pk>/met/", views.activity_met, name="activity_met"),
    path("activities/<int:pk>/join/", views.activity_join, name="activity_join"),
    path("activities/<int:pk>/leave/", views.activity_leave, name="activity_leave"),
    path("activities/<int:pk>/post/", views.activity_post, name="activity_post"),
    path("activities/<int:pk>/photo/", views.activity_photo, name="activity_photo"),
    path(
        "activities/<int:pk>/members/<int:membership_id>/vote/",
        views.membership_vote,
        name="membership_vote",
    ),
    # Interests, profile, notifications, donations
    path("interests/", views.interests, name="interests"),
    path("access/", views.access_preferences, name="access_preferences"),
    path("profile/", views.profile, name="profile"),
    path("profile/avatar/", views.avatar_upload, name="avatar_upload"),
    path("verify-age/", views.verify_age, name="verify_age"),
    path("wards/", views.wards, name="wards"),
    path("wards/invite/", views.guardian_invite_create, name="guardian_invite_create"),
    path("wards/<int:ward_pk>/revoke/", views.guardian_revoke, name="guardian_revoke"),
    path("guardianship/", views.my_guardians, name="my_guardians"),
    path(
        "guardian-invites/<str:token>/accept/",
        views.guardian_invite_accept,
        name="guardian_invite_accept",
    ),
    path(
        "guardian-invites/<str:token>/decline/",
        views.guardian_invite_decline,
        name="guardian_invite_decline",
    ),
    path("notifications/", views.notifications_list, name="notifications"),
    path("notifications/read-all/", views.notifications_read_all, name="notifications_read_all"),
    path(
        "notifications/preferences/",
        views.notification_preferences,
        name="notification_preferences",
    ),
    path("messages/", views.messages_page, name="messages"),
    path("donate/", views.donate, name="donate"),
    # Safety: reporting & blocking
    path("report/", views.report, name="report"),
    path("users/<int:pk>/block/", views.block_user_view, name="block_user"),
    path("users/<int:pk>/unblock/", views.unblock_user_view, name="unblock_user"),
    # Transparency (W1-8) & GDPR self-service
    path("privacy/", views.privacy, name="privacy"),
    path("terms/", views.terms, name="terms"),
    path("my-safety-record/", views.safety_record, name="safety_record"),
    path("account/delete/", views.account_delete, name="account_delete"),
]
