"""Base settings shared across environments."""

from pathlib import Path

import environ

BASE_DIR = Path(__file__).resolve().parent.parent.parent

env = environ.Env(
    DJANGO_DEBUG=(bool, False),
)
# Local dev reads a .env file if present; Docker/CI pass env vars directly.
environ.Env.read_env(BASE_DIR / ".env")

SECRET_KEY = env("DJANGO_SECRET_KEY", default="insecure-dev-key-change-me")
DEBUG = env.bool("DJANGO_DEBUG", default=False)
ALLOWED_HOSTS = env.list("DJANGO_ALLOWED_HOSTS", default=[])

INSTALLED_APPS = [
    # daphne must precede staticfiles so its ASGI runserver takes over (D5 chat).
    "daphne",
    "django.contrib.admin",
    "django.contrib.auth",
    "django.contrib.contenttypes",
    "django.contrib.sessions",
    "django.contrib.messages",
    "django.contrib.staticfiles",
    "django.contrib.gis",
    # Third-party
    "rest_framework",
    "rest_framework_gis",
    "django_filters",
    "drf_spectacular",
    "channels",
    # Local
    "apps.accounts",
    "apps.taxonomy",
    "apps.places",
    "apps.ingestion",
    "apps.social",
    "apps.safety",
    "apps.chat",
    "apps.booking",
    "apps.media",
    "apps.donations",
    "apps.ops",
    "apps.events",
    "apps.discovery",
]

MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    "django.middleware.common.CommonMiddleware",
    "django.middleware.csrf.CsrfViewMiddleware",
    "django.contrib.auth.middleware.AuthenticationMiddleware",
    "django.contrib.messages.middleware.MessageMiddleware",
    "django.middleware.clickjacking.XFrameOptionsMiddleware",
]

ROOT_URLCONF = "config.urls"

TEMPLATES = [
    {
        "BACKEND": "django.template.backends.django.DjangoTemplates",
        "DIRS": [],
        "APP_DIRS": True,
        "OPTIONS": {
            "context_processors": [
                "django.template.context_processors.request",
                "django.contrib.auth.context_processors.auth",
                "django.contrib.messages.context_processors.messages",
            ],
        },
    },
]

WSGI_APPLICATION = "config.wsgi.application"
ASGI_APPLICATION = "config.asgi.application"

# Postgres + PostGIS. django-environ maps the `postgis://` scheme to the
# GeoDjango backend (django.contrib.gis.db.backends.postgis).
DATABASES = {
    "default": env.db(
        "DATABASE_URL",
        default="postgis://app:app@localhost:5432/app",
    ),
}

AUTH_PASSWORD_VALIDATORS = [
    {"NAME": "django.contrib.auth.password_validation.UserAttributeSimilarityValidator"},
    {"NAME": "django.contrib.auth.password_validation.MinimumLengthValidator"},
    {"NAME": "django.contrib.auth.password_validation.CommonPasswordValidator"},
    {"NAME": "django.contrib.auth.password_validation.NumericPasswordValidator"},
]

LANGUAGE_CODE = "en-us"
TIME_ZONE = "Europe/Bucharest"
USE_I18N = True
USE_TZ = True

STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"

MEDIA_ROOT = env("MEDIA_ROOT", default=str(BASE_DIR / "media"))

DEFAULT_AUTO_FIELD = "django.db.models.BigAutoField"

AUTH_USER_MODEL = "accounts.User"

# Pluggable identity/age-assurance provider (D2). Default is the dev stub; in
# production set this to a real provider (e.g. the EUDI Wallet provider). The dev
# provider refuses to run outside DEBUG unless IDENTITY_ALLOW_DEV_PROVIDER=True.
IDENTITY_PROVIDER = env(
    "IDENTITY_PROVIDER",
    default="apps.accounts.identity.providers.dev.DevIdentityProvider",
)
IDENTITY_ALLOW_DEV_PROVIDER = env.bool("IDENTITY_ALLOW_DEV_PROVIDER", default=False)

REST_FRAMEWORK = {
    "DEFAULT_SCHEMA_CLASS": "drf_spectacular.openapi.AutoSchema",
    "DEFAULT_FILTER_BACKENDS": [
        "django_filters.rest_framework.DjangoFilterBackend",
    ],
    "DEFAULT_PAGINATION_CLASS": "rest_framework.pagination.LimitOffsetPagination",
    "PAGE_SIZE": 50,
    # Baseline anti-abuse throttling across the whole API (rates overridable via env).
    # For multi-process deploys, configure a shared cache (Redis) so counts are global.
    "DEFAULT_THROTTLE_CLASSES": [
        "rest_framework.throttling.AnonRateThrottle",
        "rest_framework.throttling.UserRateThrottle",
    ],
    "DEFAULT_THROTTLE_RATES": {
        "anon": env("DRF_THROTTLE_ANON", default="60/min"),
        "user": env("DRF_THROTTLE_USER", default="240/min"),
    },
}

SPECTACULAR_SETTINGS = {
    "TITLE": "Social Activities API",
    "DESCRIPTION": (
        "Text-first, safety-by-design platform for organizing in-person activities at "
        "real places. Children-first: EU-grade age assurance, age-cohort isolation, and "
        "strong moderation. This OpenAPI schema is the stable contract clients build "
        "against — see /api/docs/ for the interactive Swagger UI."
    ),
    "VERSION": env("APP_VERSION", default="0.1.0"),
    "SERVE_INCLUDE_SCHEMA": False,
    # Split request vs. response components so generated client models are accurate
    # (read-only/write-only fields don't bleed across).
    "COMPONENT_SPLIT_REQUEST": True,
    "SORT_OPERATIONS": False,
    "CONTACT": {
        "name": "Maintainers",
        "url": "https://github.com/DobosP/social_media_activities_app",
    },
    "LICENSE": {"name": "See repository"},
    "SERVERS": [{"url": "/", "description": "This deployment"}],
    "SWAGGER_UI_SETTINGS": {"persistAuthorization": True, "displayRequestDuration": True},
    # Curated, stable tag order grouping endpoints by domain.
    "TAGS": [
        {"name": "accounts", "description": "Identity, age band, cohort, consent."},
        {"name": "places", "description": "Real places (GeoJSON) and proximity search."},
        {"name": "taxonomy", "description": "Activity categories and the activity graph."},
        {"name": "social", "description": "Activities, threads, membership, join-by-vote."},
        {"name": "safety", "description": "Reporting, blocking, moderation."},
        {"name": "chat", "description": "Per-thread messaging (REST + WebSocket)."},
        {"name": "booking", "description": "Reservations and provider deep-links."},
        {"name": "media", "description": "Profile pictures and private thread photos."},
        {"name": "donations", "description": "Nonprofit donations (no ads/tracking)."},
        {"name": "events", "description": "Happenings associated with places."},
        {"name": "ops", "description": "Health and aggregate, privacy-respecting metrics."},
    ],
}

# --- Ingestion ---
OVERPASS_URL = env("OVERPASS_URL", default="https://overpass-api.de/api/interpreter")
INGEST_DEFAULT_CITY = env("INGEST_DEFAULT_CITY", default="Cluj-Napoca")
INGEST_USER_AGENT = env(
    "INGEST_USER_AGENT",
    default="social-activities-app/0.1 (nonprofit; contact: you@example.org)",
)

# --- D6 media ---
# Image bytes live in object storage; the local backend is the dev/test default.
MEDIA_STORAGE_BACKEND = env(
    "MEDIA_STORAGE_BACKEND", default="apps.media.storage.LocalStorageBackend"
)
MEDIA_MAX_UPLOAD_BYTES = env.int("MEDIA_MAX_UPLOAD_BYTES", default=5 * 1024 * 1024)
# Longest-side cap; larger uploads are downscaled (privacy + storage/bandwidth).
MEDIA_MAX_DIMENSION = env.int("MEDIA_MAX_DIMENSION", default=2048)
MEDIA_SIGNED_URL_TTL = env.int("MEDIA_SIGNED_URL_TTL", default=300)
# Swappable safety-scanning posture (CSAR-dependent); default matches a hash blocklist.
MEDIA_IMAGE_SCANNER = env("MEDIA_IMAGE_SCANNER", default="apps.media.scanning.HashBlocklistScanner")
MEDIA_CSAM_HASH_BLOCKLIST = env.list("MEDIA_CSAM_HASH_BLOCKLIST", default=[])

# D7 — richer place data.
# Overture places parquet path/glob (local extract or the public S3 release, e.g.
# "s3://overturemaps-us-west-2/release/<rel>/theme=places/type=place/*").
OVERTURE_DATA_PATH = env("OVERTURE_DATA_PATH", default="")
# Optional, paid Google Places enrichment — OFF by default (enrichment only, never
# a place source). Enable explicitly and provide a key to use it.
GOOGLE_PLACES_ENABLED = env.bool("GOOGLE_PLACES_ENABLED", default=False)
GOOGLE_PLACES_API_KEY = env("GOOGLE_PLACES_API_KEY", default="")

# --- D5 chat (real-time, ASGI/Channels) ---
# In-memory layer suits a single process; a multi-process deploy sets a Redis layer
# (channels-redis) here — see docs/ARCHITECTURE.md / D9 ops.
CHANNEL_LAYERS = {
    "default": {
        "BACKEND": env(
            "CHANNEL_LAYER_BACKEND",
            default="channels.layers.InMemoryChannelLayer",
        )
    }
}
# Swap to add CSAR-driven scanning/encryption without re-architecting (see COMPLIANCE).
CHAT_MESSAGE_POLICY = env("CHAT_MESSAGE_POLICY", default="apps.chat.policy.BasicMessagePolicy")
CHAT_MAX_LENGTH = env.int("CHAT_MAX_LENGTH", default=4000)
CHAT_RATE_LIMIT = env.int("CHAT_RATE_LIMIT", default=30)
CHAT_RATE_WINDOW_SECONDS = env.int("CHAT_RATE_WINDOW_SECONDS", default=60)
# 0 disables time-based purging; set a positive number of days to enable retention.
CHAT_RETENTION_DAYS = env.int("CHAT_RETENTION_DAYS", default=0)

# --- D9 donations (no ads / no tracking-based monetization) ---
# Pluggable payment provider; default builds an off-platform checkout deep link and
# stores no card data. Swap for a real EU-friendly nonprofit processor in prod.
DONATIONS_PROVIDER = env("DONATIONS_PROVIDER", default="apps.donations.providers.DeepLinkProvider")
DONATIONS_CHECKOUT_BASE_URL = env("DONATIONS_CHECKOUT_BASE_URL", default="")
# Shared secret verifying provider webhook callbacks (empty disables the check in dev).
DONATIONS_WEBHOOK_SECRET = env("DONATIONS_WEBHOOK_SECRET", default="")

# Build/version surfaced by /healthz (set from CI / image tag).
APP_VERSION = env("APP_VERSION", default="0.1.0")
