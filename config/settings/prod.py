"""Production settings. ALLOWED_HOSTS / secrets come from the environment."""

import copy

from django.core.exceptions import ImproperlyConfigured

from .base import *  # noqa: F401,F403
from .base import DATABASES, MIDDLEWARE, env

DEBUG = False

# Fail closed on secrets. base.py ships a dev-only SECRET_KEY default so local/test
# runs work without configuration; in production a missing or sentinel key must be a
# hard boot error, never a silently world-readable signer (a known key lets an
# attacker forge session cookies, password-reset tokens, and signed media URLs).
SECRET_KEY = env("DJANGO_SECRET_KEY")  # no default -> ImproperlyConfigured if unset
if SECRET_KEY == "insecure-dev-key-change-me":
    raise ImproperlyConfigured(
        "DJANGO_SECRET_KEY is still the insecure development default; set a unique "
        "secret in the production environment."
    )

# Work on our own copy so prod-only DB tweaks below never mutate the dict shared
# with base/test settings (importing this module must have no side effects).
DATABASES = copy.deepcopy(DATABASES)

# Security hardening (override per-deployment via env).
SECURE_SSL_REDIRECT = env.bool("DJANGO_SECURE_SSL_REDIRECT", default=True)
SESSION_COOKIE_SECURE = True
CSRF_COOKIE_SECURE = True
SECURE_HSTS_SECONDS = env.int("DJANGO_HSTS_SECONDS", default=3600)
SECURE_HSTS_INCLUDE_SUBDOMAINS = True
SECURE_HSTS_PRELOAD = True
SECURE_PROXY_SSL_HEADER = ("HTTP_X_FORWARDED_PROTO", "https")

# Managed Postgres (e.g. Render) hands us a `postgres://` URL; this app is PostGIS,
# so force the GeoDjango backend regardless of the URL scheme.
DATABASES["default"]["ENGINE"] = "django.contrib.gis.db.backends.postgis"  # noqa: F405

# Production DB resilience (threat model finding #2): cap runaway queries as a DoS
# guard, reuse connections, and health-check pooled connections (Django 4.1+).
DATABASES["default"].setdefault("OPTIONS", {})  # noqa: F405
DATABASES["default"]["OPTIONS"]["options"] = (  # noqa: F405
    f"-c statement_timeout={env.int('DB_STATEMENT_TIMEOUT_MS', default=30000)}"
)
DATABASES["default"]["CONN_MAX_AGE"] = env.int("DB_CONN_MAX_AGE", default=60)  # noqa: F405
DATABASES["default"]["CONN_HEALTH_CHECKS"] = True  # noqa: F405

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
