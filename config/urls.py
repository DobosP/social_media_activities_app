from django.contrib import admin
from django.urls import include, path
from drf_spectacular.views import SpectacularAPIView, SpectacularSwaggerView

urlpatterns = [
    path("admin/", admin.site.urls),
    path("api/accounts/", include("apps.accounts.urls")),
    path("api/places/", include("apps.places.urls")),
    path("api/taxonomy/", include("apps.taxonomy.urls")),
    path("api/social/", include("apps.social.urls")),
    path("api/safety/", include("apps.safety.urls")),
    path("api/chat/", include("apps.chat.urls")),
    path("api/booking/", include("apps.booking.urls")),
    path("api/media/", include("apps.media.urls")),
    path("api/schema/", SpectacularAPIView.as_view(), name="schema"),
    path("api/docs/", SpectacularSwaggerView.as_view(url_name="schema"), name="docs"),
]
