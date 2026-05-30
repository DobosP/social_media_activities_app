from django.contrib import admin
from django.urls import include, path
from drf_spectacular.views import SpectacularAPIView, SpectacularSwaggerView

from apps.ops.views import HealthView

urlpatterns = [
    path("healthz", HealthView.as_view(), name="healthz"),
    path("admin/", admin.site.urls),
    path("api/accounts/", include("apps.accounts.urls")),
    path("api/places/", include("apps.places.urls")),
    path("api/taxonomy/", include("apps.taxonomy.urls")),
    path("api/social/", include("apps.social.urls")),
    path("api/safety/", include("apps.safety.urls")),
    path("api/chat/", include("apps.chat.urls")),
    path("api/messaging/", include("apps.messaging.urls")),
    path("api/booking/", include("apps.booking.urls")),
    path("api/media/", include("apps.media.urls")),
    path("api/donations/", include("apps.donations.urls")),
    path("api/ops/", include("apps.ops.urls")),
    path("api/events/", include("apps.events.urls")),
    path("api/discovery/", include("apps.discovery.urls")),
    path("api/notifications/", include("apps.notifications.urls")),
    path("api/recommendations/", include("apps.recommendations.urls")),
    path("api/connections/", include("apps.connections.urls")),
    path("api/schema/", SpectacularAPIView.as_view(), name="schema"),
    path("api/docs/", SpectacularSwaggerView.as_view(url_name="schema"), name="docs"),
    # Server-rendered web UI (mounted at the root; must come after the API/admin routes).
    path("", include("apps.web.urls")),
]
