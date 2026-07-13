from django.conf import settings
from django.contrib.auth import views as auth_views
from django.urls import path
from django.views.decorators.cache import cache_control

from apps.events.feeds import UpcomingEventsAtomFeed, UpcomingEventsFeed

from . import views
from .seo import PUBLIC_CACHE_SECONDS

# Anonymous open-data syndication feeds — publicly cacheable for crawl-budget/CDN friendliness.
_feed_cache = cache_control(public=True, max_age=PUBLIC_CACHE_SECONDS)

urlpatterns = [
    path("", views.home, name="home"),
    # F38: offline-resilient "my next meetups" + its root-scoped service worker (/sw.js).
    path("my-meetups/", views.my_meetups, name="my_meetups"),
    # W4-F18: self-only venue data-quality digest for the meetups you're going to.
    path("my-venues/", views.my_venues, name="my_venues"),
    path("sw.js", views.service_worker, name="service_worker"),
    # W2-F5: organizer console — what each activity/series/group you run needs now.
    path("organize/", views.organize, name="organize"),
    # Auth
    path("register/", views.register, name="register"),
    path("login/", views.ThrottledLoginView.as_view(), name="login"),
    path("logout/", auth_views.LogoutView.as_view(), name="logout"),
    # DSA Art.17 redress for a restricted account (reachable without a session).
    path("account/restricted/", views.account_restricted, name="account_restricted"),
    # Discover
    path("places/", views.places_map, name="places_map"),
    path("places/list/", views.places_list, name="places_list"),
    path("places/propose/", views.place_propose, name="place_propose"),
    path("places/<int:pk>/claim/", views.place_claim, name="place_claim"),
    path(
        "places/<int:pk>/official-image/",
        views.place_official_image,
        name="place_official_image",
    ),
    path("places/pending/", views.places_pending, name="places_pending"),
    path(
        "places/pending/<int:proposal_id>/confirm/",
        views.place_confirm,
        name="place_confirm",
    ),
    path("places/<int:pk>/", views.place_detail, name="place_detail"),
    path("places/<int:pk>/edges/<int:edge_id>/vote/", views.edge_vote, name="edge_vote"),
    path("places/<int:pk>/facts/vote/", views.fact_vote, name="fact_vote"),
    path(
        "places/<int:pk>/corrections/propose/",
        views.place_correction_propose,
        name="place_correction_propose",
    ),
    path(
        "places/<int:pk>/corrections/<int:correction_id>/confirm/",
        views.place_correction_confirm,
        name="place_correction_confirm",
    ),
    path(
        "places/<int:pk>/hours-wrong/",
        views.place_open_now_report,
        name="place_open_now_report",
    ),
    path(
        "places/<int:pk>/hours-reset/",
        views.place_open_now_reset,
        name="place_open_now_reset",
    ),
    path(
        "places/<int:pk>/closed/",
        views.place_closure_report,
        name="place_closure_report",
    ),
    path(
        "places/<int:pk>/closed-reset/",
        views.place_closure_reset,
        name="place_closure_reset",
    ),
    # Keyword-rich canonical slug form (AFTER the specific place action routes so a slug like
    # "hours-wrong" can't shadow them). The bare /places/<pk>/ 301s here.
    path("places/<int:pk>/<slug:slug>/", views.place_detail, name="place_detail_slug"),
    # Events syndication feed (literal "feed" can't match <int:pk>, so order is safe).
    path("events/feed/", _feed_cache(UpcomingEventsFeed()), name="events_feed"),
    path("events/feed/atom/", _feed_cache(UpcomingEventsAtomFeed()), name="events_feed_atom"),
    path("events/", views.events_list, name="events_list"),
    path("events/<int:pk>/", views.event_detail, name="event_detail"),
    path("events/<int:pk>/report/", views.event_report, name="event_report"),
    path("events/<int:pk>/report-reset/", views.event_report_reset, name="event_report_reset"),
    path("events/<int:pk>/<slug:slug>/", views.event_detail, name="event_detail_slug"),
    # Public city×activity landing pages ("things to do in <city>") — keyword-rich, namespaced.
    path("things-to-do/", views.things_to_do_index, name="things_to_do_index"),
    path("things-to-do/<slug:area_slug>/", views.things_to_do_city, name="things_to_do_city"),
    path(
        "things-to-do/<slug:area_slug>/<slug:activity_slug>/",
        views.things_to_do,
        name="things_to_do",
    ),
    # Activities
    path("activities/", views.activity_list, name="activity_list"),
    path("activities/new/", views.activity_create, name="activity_create"),
    # F4: recurring activity series (owner-managed templates)
    path("activities/series/", views.series_list, name="series_list"),
    path("activities/series/new/", views.series_create, name="series_create"),
    path("activities/series/<int:pk>/", views.series_detail, name="series_detail"),
    path("activities/series/<int:pk>/pause/", views.series_pause, name="series_pause"),
    path("activities/series/<int:pk>/resume/", views.series_resume, name="series_resume"),
    path(
        "activities/series/<int:pk>/next-note/",
        views.series_set_next_note,
        name="series_set_next_note",
    ),
    path("activities/series/<int:pk>/end/", views.series_end, name="series_end"),
    path("activities/<int:pk>/", views.activity_detail, name="activity_detail"),
    path("activities/<int:pk>/edit/", views.activity_edit, name="activity_edit"),
    path("activities/<int:pk>/cancel/", views.activity_cancel, name="activity_cancel"),
    path("activities/<int:pk>/announce/", views.activity_announce, name="activity_announce"),
    path(
        "activities/<int:pk>/supervisor/add/",
        views.activity_add_supervisor,
        name="activity_add_supervisor",
    ),
    path(
        "activities/<int:pk>/supervision/",
        views.activity_set_supervision,
        name="activity_set_supervision",
    ),
    path(
        "activities/<int:pk>/co-org/grant/", views.activity_grant_coorg, name="activity_grant_coorg"
    ),
    path(
        "activities/<int:pk>/co-org/revoke/",
        views.activity_revoke_coorg,
        name="activity_revoke_coorg",
    ),
    path(
        "activities/<int:pk>/transfer/",
        views.activity_transfer_owner,
        name="activity_transfer_owner",
    ),
    path("activities/<int:pk>/rsvp/", views.activity_rsvp, name="activity_rsvp"),
    path(
        "activities/<int:pk>/support-person/",
        views.activity_support_companion,
        name="activity_support_companion",
    ),
    path("activities/<int:pk>/arrived/", views.activity_arrived, name="activity_arrived"),
    path("activities/<int:pk>/transit/", views.activity_transit, name="activity_transit"),
    path("activities/<int:pk>/departing/", views.activity_departing, name="activity_departing"),
    path("activities/<int:pk>/met/", views.activity_met, name="activity_met"),
    path("activities/<int:pk>/join/", views.activity_join, name="activity_join"),
    path("activities/<int:pk>/leave/", views.activity_leave, name="activity_leave"),
    path("activities/<int:pk>/unsafe/", views.activity_unsafe, name="activity_unsafe"),
    path("activities/<int:pk>/post/", views.activity_post, name="activity_post"),
    path("share/", views.share_to_thread, name="share_to_thread"),
    path(
        "activities/<int:pk>/post/<int:post_id>/edit/",
        views.activity_post_edit,
        name="activity_post_edit",
    ),
    path(
        "activities/<int:pk>/post/<int:post_id>/delete/",
        views.activity_post_delete,
        name="activity_post_delete",
    ),
    path(
        "activities/<int:pk>/post/<int:post_id>/react/",
        views.activity_post_react,
        name="activity_post_react",
    ),
    path("activities/<int:pk>/photo/", views.activity_photo, name="activity_photo"),
    path(
        "activities/<int:pk>/members/<int:membership_id>/vote/",
        views.membership_vote,
        name="membership_vote",
    ),
    # F3 saved-search alerts
    path("saved-searches/", views.saved_searches_page, name="saved_searches"),
    path("saved-searches/create/", views.saved_search_create, name="saved_search_create"),
    path("saved-searches/<int:pk>/delete/", views.saved_search_delete, name="saved_search_delete"),
    # Consolidated nav hubs (presentation-only landings; see views)
    path("you/", views.you_hub, name="you"),
    path("settings/", views.settings_hub, name="settings"),
    path("settings/api-token/revoke/", views.api_token_revoke, name="api_token_revoke"),
    path("inbox/", views.inbox_hub, name="inbox"),
    # Interests, profile, notifications, donations
    path("interests/", views.interests, name="interests"),
    path("topics/", views.topic_preferences, name="topic_preferences"),
    path("access/", views.access_preferences, name="access_preferences"),
    path("profile/", views.profile, name="profile"),
    path("profile/avatar/", views.avatar_upload, name="avatar_upload"),
    path("profile/avatar-style/", views.avatar_style, name="avatar_style"),
    # ADR-0028: another user's tier-gated profile page + the hover-overview partial.
    path("people/<uuid:public_id>/", views.person, name="person"),
    path("people/<uuid:public_id>/card/", views.person_card, name="person_card"),
    path("verify-age/", views.verify_age, name="verify_age"),
    path("wards/", views.wards, name="wards"),
    path("wards/invite/", views.guardian_invite_create, name="guardian_invite_create"),
    path("wards/<int:ward_pk>/revoke/", views.guardian_revoke, name="guardian_revoke"),
    path(
        "wards/<int:ward_pk>/limits/",
        views.guardian_guardrail_set,
        name="guardian_guardrail_set",
    ),
    path(
        "wards/<int:ward_pk>/topics/",
        views.ward_topics_set,
        name="ward_topics_set",
    ),
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
    # Groups (persistent, cohort-pinned, joinable standing groups)
    path("groups/", views.group_list, name="groups"),
    path("groups/new/", views.group_create, name="group_create"),
    path("groups/<int:pk>/", views.group_detail, name="group_detail"),
    path("groups/<int:pk>/join/", views.group_join, name="group_join"),
    path("groups/<int:pk>/leave/", views.group_leave, name="group_leave"),
    path("groups/<int:pk>/post/", views.group_post, name="group_post"),
    path("groups/<int:pk>/announce/", views.group_announce, name="group_announce"),
    path("groups/<int:pk>/ask/", views.group_ask, name="group_ask"),
    path("groups/<int:pk>/archive/", views.group_archive, name="group_archive"),
    # F27 gauge-interest (ephemeral proto-meetups)
    path("gauges/", views.gauges, name="gauges"),
    path("gauges/new/", views.gauge_create, name="gauge_create"),
    path("gauges/<int:pk>/", views.gauge_detail, name="gauge_detail"),
    path("gauges/<int:pk>/interested/", views.gauge_interested, name="gauge_interested"),
    path("gauges/<int:pk>/uninterested/", views.gauge_uninterested, name="gauge_uninterested"),
    path("gauges/<int:pk>/convert/", views.gauge_convert, name="gauge_convert"),
    # Communities
    path("communities/", views.communities_page, name="communities"),
    path("communities/graph/", views.community_graph_page, name="community_graph"),
    path("communities/<slug:slug>/", views.community_detail, name="community_detail"),
    # Connections
    path("connections/", views.connections_page, name="connections"),
    path("connections/request/", views.connection_request, name="connection_request"),
    path("connections/<int:pk>/respond/", views.connection_respond, name="connection_respond"),
    path("connections/<int:pk>/withdraw/", views.connection_withdraw, name="connection_withdraw"),
    path("connections/remove/", views.connection_remove, name="connection_remove"),
    path("connections/message/", views.connection_message, name="connection_message"),
    path("donate/", views.donate, name="donate"),
    path("transparency/", views.transparency, name="transparency"),
    path("my-donations/", views.my_donations, name="my_donations"),
    path("campaigns/", views.campaigns, name="campaigns"),
    path("partners/", views.partners_list, name="partners"),
    # Open data: what the dataset is + machine-access links, plus whitelisted bulk snapshots.
    path("open-data/", views.open_data, name="open_data"),
    path("open-data/snapshot/<str:name>", views.open_data_snapshot, name="open_data_snapshot"),
    # Public (logged-out) discovery of adult activities & groups
    path("discover/", views.discover, name="discover"),
    path(
        "activities/<int:pk>/listing/",
        views.activity_listing_toggle,
        name="activity_listing_toggle",
    ),
    path("groups/<int:pk>/listing/", views.group_listing_toggle, name="group_listing_toggle"),
    # Safety: reporting & blocking
    path("report/", views.report, name="report"),
    path("users/<int:pk>/block/", views.block_user_view, name="block_user"),
    path("users/<int:pk>/unblock/", views.unblock_user_view, name="unblock_user"),
    # Transparency (W1-8) & GDPR self-service
    path("display/", views.display_preferences, name="display_preferences"),
    path("privacy/", views.privacy, name="privacy"),
    path("terms/", views.terms, name="terms"),
    path("my-privacy/", views.my_privacy, name="my_privacy"),
    path("my-activity-log/", views.activity_log, name="activity_log"),
    path("my-safety-record/", views.safety_record, name="safety_record"),
    path("my-safety-record/contest/", views.safety_record_appeal, name="safety_record_appeal"),
    path("account/export/", views.account_export, name="account_export"),
    path("account/calendar.ics", views.my_calendar, name="my_calendar"),  # W3-F18 self-only .ics
    path("account/delete/", views.account_delete, name="account_delete"),
]

# DEBUG-only: Phase-1 React/Vite pipeline proof (Aurora design preview). Real SPA
# routes arrive with redesign Phase 2 and will not be DEBUG-gated.
if settings.DEBUG:
    urlpatterns += [path("app/preview/", views.spa_preview, name="spa_preview")]
