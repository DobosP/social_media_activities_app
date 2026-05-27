"""Production settings. ALLOWED_HOSTS / secrets come from the environment."""

from .base import *  # noqa: F401,F403
from .base import MIDDLEWARE, env

DEBUG = False

# Security hardening (override per-deployment via env).
SECURE_SSL_REDIRECT = env.bool("DJANGO_SECURE_SSL_REDIRECT", default=True)
SESSION_COOKIE_SECURE = True
CSRF_COOKIE_SECURE = True
SECURE_HSTS_SECONDS = env.int("DJANGO_HSTS_SECONDS", default=3600)
SECURE_HSTS_INCLUDE_SUBDOMAINS = True
SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", "https")

# Managed Postgres (e.g. Render) hands us a `postgres://` URL; this app is PostGIS,
# so force the GeoDjango backend regardless of the URL scheme.
DATABASES["default"]["ENGINE"] = "django.contrib.gis.db.backends.postgis"  # noqa: F405

# Render injects the public hostname at runtime; trust it for hosts + CSRF.
RENDER_EXTERNAL_HOSTNAME = env("RENDER_EXTERNAL_HOSTNAME", default="")
if RENDER_EXTERNAL_HOSTNAME:
    ALLOWED_HOSTS = [RENDER_EXTERNAL_HOSTNAME]
    CSRF_TRUSTED_ORIGINS = [f"https://{RENDER_EXTERNAL_HOSTNAME}"]

# Serve static assets (admin, DRF/Swagger UI) from the app process via WhiteNoise,
# so a single container needs no separate web server or CDN for a demo deploy.
# WhiteNoise must sit immediately after SecurityMiddleware (index 0).
MIDDLEWARE = [MIDDLEWARE[0], "whitenoise.middleware.WhiteNoiseMiddleware", *MIDDLEWARE[1:]]
STORAGES = {
    "default": {"BACKEND": "django.core.files.storage.FileSystemStorage"},
    "staticfiles": {"BACKEND": "whitenoise.storage.CompressedStaticFilesStorage"},
}
