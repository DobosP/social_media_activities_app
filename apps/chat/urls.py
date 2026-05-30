# The chat app exposes no HTTP routes after the "One Thread" unification — realtime delivery
# is over the WebSocket consumer (see apps/chat/routing.py); thread history is server-rendered
# (web) or read via the DRF activities `posts` action (apps/social). The empty include is kept
# so config/urls.py's `path("api/chat/", include("apps.chat.urls"))` stays valid.
urlpatterns = []
