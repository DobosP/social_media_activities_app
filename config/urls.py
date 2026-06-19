from django.contrib import admin
from django.urls import include, path
from drf_spectacular.views import SpectacularAPIView, SpectacularSwaggerView

from apps.accounts.views import ObtainAPIToken
from apps.ops.views import HealthView, ReadyView

# The API surface, mounted ONCE here and exposed under two prefixes below:
#   * /api/v1/  — the CANONICAL, versioned base. New clients build against this; a future breaking
#                 change ships as /api/v2/ alongside it (the views are version-agnostic today).
#   * /api/     — a backward-compatible UNVERSIONED alias so existing clients/tests keep working.
# A literal "v1" segment (not a captured <version> kwarg) is used deliberately: a path kwarg would
# leak into every view handler's signature and break the many APIViews with explicit args.
api_patterns = [
    path("accounts/", include("apps.accounts.urls")),
    # W10 mobile auth: opaque-token obtain/revoke (no JWT; throttled hard).
    path("auth/token/", ObtainAPIToken.as_view(), name="api-token"),
    path("places/", include("apps.places.urls")),
    path("taxonomy/", include("apps.taxonomy.urls")),
    path("social/", include("apps.social.urls")),
    path("safety/", include("apps.safety.urls")),
    path("chat/", include("apps.chat.urls")),
    path("messaging/", include("apps.messaging.urls")),
    path("booking/", include("apps.booking.urls")),
    path("media/", include("apps.media.urls")),
    path("donations/", include("apps.donations.urls")),
    path("ops/", include("apps.ops.urls")),
    path("events/", include("apps.events.urls")),
    path("ingestion/", include("apps.ingestion.urls")),
    path("discovery/", include("apps.discovery.urls")),
    path("notifications/", include("apps.notifications.urls")),
    path("recommendations/", include("apps.recommendations.urls")),
    path("connections/", include("apps.connections.urls")),
    path("communities/", include("apps.communities.urls")),
    path("saved-searches/", include("apps.saved_searches.urls")),
]

urlpatterns = [
    path("healthz", HealthView.as_view(), name="healthz"),  # liveness (process up)
    path("readyz", ReadyView.as_view(), name="readyz"),  # readiness (DB + configured shared deps)
    path("admin/", admin.site.urls),
    # Versioned canonical FIRST so /api/v1/... resolves here; the alias then catches the rest.
    path("api/v1/", include(api_patterns)),
    path("api/", include(api_patterns)),
    path("api/schema/", SpectacularAPIView.as_view(), name="schema"),
    path("api/docs/", SpectacularSwaggerView.as_view(url_name="schema"), name="docs"),
    # Language switcher (P6/IS-7): set_language persists the choice (cookie/session) and
    # LocaleMiddleware then serves Romanian. Open-redirect-safe (set_language validates `next`).
    path("i18n/", include("django.conf.urls.i18n")),
    # Server-rendered web UI (mounted at the root; must come after the API/admin routes).
    path("", include("apps.web.urls")),
]
