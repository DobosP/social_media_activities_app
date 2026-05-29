from django.contrib.auth import views as auth_views
from django.urls import path

from . import views

urlpatterns = [
    path("", views.home, name="home"),
    # Auth
    path("register/", views.register, name="register"),
    path("login/", auth_views.LoginView.as_view(template_name="web/login.html"), name="login"),
    path("logout/", auth_views.LogoutView.as_view(), name="logout"),
    # Discover
    path("places/", views.places_map, name="places_map"),
    path("places/<int:pk>/", views.place_detail, name="place_detail"),
    path("events/", views.events_list, name="events_list"),
    path("events/<int:pk>/", views.event_detail, name="event_detail"),
    # Activities
    path("activities/", views.activity_list, name="activity_list"),
    path("activities/new/", views.activity_create, name="activity_create"),
    path("activities/<int:pk>/", views.activity_detail, name="activity_detail"),
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
    path("profile/", views.profile, name="profile"),
    path("profile/avatar/", views.avatar_upload, name="avatar_upload"),
    path("verify-age/", views.verify_age, name="verify_age"),
    path("wards/", views.wards, name="wards"),
    path("notifications/", views.notifications_list, name="notifications"),
    path("notifications/read-all/", views.notifications_read_all, name="notifications_read_all"),
    path("donate/", views.donate, name="donate"),
]
