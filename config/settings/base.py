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
    "apps.messaging",
    "apps.booking",
    "apps.media",
    "apps.donations",
    "apps.ops",
    "apps.events",
    "apps.discovery",
    "apps.notifications",
    "apps.recommendations",
    "apps.connections",
    "apps.communities",
    "apps.web",
]

MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    # Reject oversized request bodies (by Content-Length) before anything reads the stream.
    "apps.ops.middleware.MaxBodySizeMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    # Selects the language from the Accept-Language header (RO/EN) per request (P6/IS-7).
    "django.middleware.locale.LocaleMiddleware",
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
        "DIRS": [BASE_DIR / "templates"],
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

LANGUAGE_CODE = "en"
TIME_ZONE = "Europe/Bucharest"
USE_I18N = True
USE_TZ = True

# Supported locales (P6/IS-7). Romanian first — the launch city is Cluj-Napoca. The
# active language is negotiated from the Accept-Language header by LocaleMiddleware.
LANGUAGES = [
    ("en", "English"),
    ("ro", "Română"),
]
LOCALE_PATHS = [BASE_DIR / "locale"]

STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"
STATICFILES_DIRS = [BASE_DIR / "static"]

# Server-rendered web UI (apps/web) auth flow.
LOGIN_URL = "login"
LOGIN_REDIRECT_URL = "home"
LOGOUT_REDIRECT_URL = "home"

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

# EUDI Wallet / OpenID4VP age verification (D2). The verifier checks an age attestation's
# ES256 signature against EUDI_TRUSTED_ISSUERS (the EU trust list in production). Sandbox
# mode additionally trusts a local test issuer so the flow is exercisable before the live
# national wallet ships (RO ~Dec 2026).
EUDI_CLIENT_ID = env("EUDI_CLIENT_ID", default="social-activities-app")
EUDI_SANDBOX = env.bool("EUDI_SANDBOX", default=DEBUG)
EUDI_SANDBOX_ISSUER_KEY_PEM = env("EUDI_SANDBOX_ISSUER_KEY_PEM", default="")
# {issuer_id: PEM public key} — the trust anchor; populated from the EU trust list in prod.
EUDI_TRUSTED_ISSUERS = env.json("EUDI_TRUSTED_ISSUERS", default={})

# Whether minors can be onboarded (guardian-linked + consented) on this deployment.
# The current guardian-link flow establishes a relationship on mutual confirmation but does
# NOT cryptographically prove a real parent-child / legal-guardianship relationship, and no
# production-grade age/parental-responsibility trust anchor exists yet (EUDI wallets not live;
# the EU age-verification app was bypassed). So this is ON for dev/test but prod.py defaults
# it OFF — a prod deploy runs adults-only until a real trust anchor (EUDI guardian flow /
# national eID / blessed out-of-band process) is wired. See docs/AUDIT_STRESS_2026-05-29.md.
ALLOW_MINOR_ONBOARDING = env.bool("ALLOW_MINOR_ONBOARDING", default=True)

# Guardianship link invites (verified-adult → minor, mutually confirmed). How long an
# unaccepted invite stays valid, and anti-abuse limits on issuing invites.
GUARDIAN_INVITE_TTL_DAYS = env.int("GUARDIAN_INVITE_TTL_DAYS", default=7)
GUARDIAN_INVITE_RATE_LIMIT = env.int("GUARDIAN_INVITE_RATE_LIMIT", default=20)
GUARDIAN_INVITE_RATE_WINDOW_SECONDS = env.int("GUARDIAN_INVITE_RATE_WINDOW_SECONDS", default=3600)

# Number of trusted reverse proxies in front of the app (e.g. Render's edge = 1). Used for
# both DRF throttle identity AND the web login-lockout's real-client-IP derivation, so they
# agree on which X-Forwarded-For hop to trust (a spoofed XFF must not mint a fresh bucket).
NUM_PROXIES = env.int("NUM_PROXIES", default=1)

REST_FRAMEWORK = {
    "DEFAULT_SCHEMA_CLASS": "drf_spectacular.openapi.AutoSchema",
    # Deny-by-default: every endpoint requires auth unless it explicitly opts into AllowAny
    # (the intentionally public ones: places, taxonomy, discovery feeds, donations totals,
    # ops health). This prevents a future viewset from silently inheriting AllowAny.
    "DEFAULT_PERMISSION_CLASSES": [
        "rest_framework.permissions.IsAuthenticated",
    ],
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
    # Trust only the last NUM_PROXIES X-Forwarded-For hops for throttle identity — otherwise
    # a client can spoof XFF to get a fresh anon bucket per request and bypass rate limits.
    "NUM_PROXIES": NUM_PROXIES,
}

# Hard cap on request body size (bytes). Django's DATA_UPLOAD_MAX_MEMORY_SIZE governs form
# parsing but NOT DRF's JSON parser reading request.body, so a multi-MB JSON POST could OOM
# the single ASGI process. MaxBodySizeMiddleware rejects oversized requests by Content-Length
# before the body is read. Default leaves headroom for the largest legit upload (media).
MAX_REQUEST_BODY_BYTES = env.int("MAX_REQUEST_BODY_BYTES", default=8 * 1024 * 1024)
DATA_UPLOAD_MAX_MEMORY_SIZE = env.int("DATA_UPLOAD_MAX_MEMORY_SIZE", default=MAX_REQUEST_BODY_BYTES)

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
    # The OpenAPI schema + Swagger UI stay publicly readable despite deny-by-default perms.
    "SERVE_PERMISSIONS": ["rest_framework.permissions.AllowAny"],
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
        {
            "name": "messaging",
            "description": (
                "Cohort-safe, invite-accept, end-to-end-encrypted direct & group "
                "messaging by username (zero-knowledge relay)."
            ),
        },
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
# Wikidata SPARQL endpoint for the no-key website enricher (CC0).
WIKIDATA_SPARQL_URL = env("WIKIDATA_SPARQL_URL", default="https://query.wikidata.org/sparql")

# --- D6 media ---
# Image bytes live in object storage; the local backend is the dev/test default.
MEDIA_STORAGE_BACKEND = env(
    "MEDIA_STORAGE_BACKEND", default="apps.media.storage.LocalStorageBackend"
)
MEDIA_MAX_UPLOAD_BYTES = env.int("MEDIA_MAX_UPLOAD_BYTES", default=5 * 1024 * 1024)
# Longest-side cap; larger uploads are downscaled (privacy + storage/bandwidth).
MEDIA_MAX_DIMENSION = env.int("MEDIA_MAX_DIMENSION", default=2048)
# Decompression-bomb ceiling: reject images whose header-declared pixel count exceeds
# this before any pixels are decoded (≈30 MP default — above real photos, below a bomb).
MEDIA_MAX_IMAGE_PIXELS = env.int("MEDIA_MAX_IMAGE_PIXELS", default=30_000_000)
MEDIA_SIGNED_URL_TTL = env.int("MEDIA_SIGNED_URL_TTL", default=300)
# Swappable safety-scanning posture (CSAR-dependent); default matches a hash blocklist.
MEDIA_IMAGE_SCANNER = env("MEDIA_IMAGE_SCANNER", default="apps.media.scanning.HashBlocklistScanner")
MEDIA_CSAM_HASH_BLOCKLIST = env.list("MEDIA_CSAM_HASH_BLOCKLIST", default=[])
# Optional path to a newline-delimited file of known-bad SHA-256 hashes (e.g. a CSAM hash
# set from a lawful provider). Lets the blocklist be populated operationally — without
# inlining thousands of hashes in env — to make uploads safe to enable. Hashes from both
# the inline list and this file are matched.
MEDIA_CSAM_HASH_BLOCKLIST_FILE = env("MEDIA_CSAM_HASH_BLOCKLIST_FILE", default="")
# Managed scanning service (used when MEDIA_IMAGE_SCANNER=apps.media.scanning.ManagedScanner):
# the upload's SHA-256 (hash-only, privacy-preserving) is POSTed for screening over the
# SSRF-safe channel. Exact-hash matching only — not perceptual; see scanning.py.
MEDIA_SCANNER_ENDPOINT = env("MEDIA_SCANNER_ENDPOINT", default="")
MEDIA_SCANNER_API_KEY = env("MEDIA_SCANNER_API_KEY", default="")
MEDIA_SCANNER_TIMEOUT = env.int("MEDIA_SCANNER_TIMEOUT", default=10)
# Fail closed: refuse photo uploads unless an *effective* scanner is configured. The
# default HashBlocklistScanner is only effective with a non-empty blocklist (inline or
# file); or point MEDIA_IMAGE_SCANNER at a managed service. dev/test settings set False.
MEDIA_REQUIRE_SCANNER = env.bool("MEDIA_REQUIRE_SCANNER", default=True)

# Thread attachments (images + PDF in the activity conversation). Master switch; per-attachment
# size cap; and the cohorts allowed to share a FILE/PDF (a NEW media type → adults only at
# launch, "none for minors"; images are allowed in any cohort thread).
# INVARIANT: MEDIA_ATTACHMENT_MAX_BYTES + multipart/CSRF overhead must stay UNDER
# MAX_REQUEST_BODY_BYTES (above, default 8 MiB), or the MaxBodySizeMiddleware rejects the upload
# with a raw 413 before the view's friendly "too large" message. 7 MiB leaves ~1 MiB headroom.
MEDIA_ATTACHMENTS_ENABLED = env.bool("MEDIA_ATTACHMENTS_ENABLED", default=True)
MEDIA_ATTACHMENT_MAX_BYTES = env.int("MEDIA_ATTACHMENT_MAX_BYTES", default=7 * 1024 * 1024)
MEDIA_FILE_COHORTS = env.list("MEDIA_FILE_COHORTS", default=["adult"])

# D7 — richer place data.
# Overture places parquet path/glob (local extract or the public S3 release, e.g.
# "s3://overturemaps-us-west-2/release/<rel>/theme=places/type=place/*").
OVERTURE_DATA_PATH = env("OVERTURE_DATA_PATH", default="")
# Optional, paid Google Places enrichment — OFF by default (enrichment only, never
# a place source). Enable explicitly and provide a key to use it.
GOOGLE_PLACES_ENABLED = env.bool("GOOGLE_PLACES_ENABLED", default=False)
GOOGLE_PLACES_API_KEY = env("GOOGLE_PLACES_API_KEY", default="")

# --- Shared cache + real-time channel layer ---
# A single Redis (REDIS_URL) backs BOTH the cache (DRF throttles + the anti-abuse rate
# limiter in apps/safety) AND the Channels layer, so limits and WebSocket fan-out are
# GLOBAL across processes/instances. Without REDIS_URL we fall back to per-process
# LocMemCache + InMemoryChannelLayer — single-process dev only; prod.py asserts in
# production that these per-process backends are NOT in use (see config/settings/prod.py).
REDIS_URL = env("REDIS_URL", default="")
if REDIS_URL:
    CACHES = {
        "default": {
            "BACKEND": "django.core.cache.backends.redis.RedisCache",
            "LOCATION": REDIS_URL,
        }
    }
    CHANNEL_LAYERS = {
        "default": {
            "BACKEND": "channels_redis.core.RedisChannelLayer",
            "CONFIG": {"hosts": [REDIS_URL]},
        }
    }
else:
    CACHES = {"default": {"BACKEND": "django.core.cache.backends.locmem.LocMemCache"}}
    CHANNEL_LAYERS = {
        "default": {
            "BACKEND": env("CHANNEL_LAYER_BACKEND", default="channels.layers.InMemoryChannelLayer")
        }
    }
# The "One Thread" stream: a single durable social.Post conversation per activity. The
# MessagePolicy (CSAR seam) + length cap apply to EVERY write (web/DRF/socket) via
# social.post_to_thread. CHAT_MAX_LENGTH stays the canonical body cap (== POST_BODY_MAX_LENGTH).
# Swap to add CSAR-driven scanning/encryption without re-architecting (see COMPLIANCE).
CHAT_MESSAGE_POLICY = env("CHAT_MESSAGE_POLICY", default="apps.chat.policy.BasicMessagePolicy")
CHAT_MAX_LENGTH = env.int("CHAT_MAX_LENGTH", default=4000)
# Per-user thread-post rate limit (fixed window). Defaults preserve the old chat limits.
THREAD_POST_RATE_LIMIT = env.int("THREAD_POST_RATE_LIMIT", default=30)
THREAD_POST_RATE_WINDOW_SECONDS = env.int("THREAD_POST_RATE_WINDOW_SECONDS", default=60)
# Hard ceiling on the thread read window (web + DRF), so a long thread can't dump unbounded.
SOCIAL_THREAD_POST_LIMIT = env.int("SOCIAL_THREAD_POST_LIMIT", default=100)
# NOTE: thread messages are now permanent + audited (no time-based purge) — the child-safety-
# correct retention posture. The former CHAT_RETENTION_DAYS / purge_chat job were removed.

# --- Connections (find + reconnect with people you've shared real activities with) ---
# Cohorts allowed to use connections, each WITHIN its own cohort. Cross-age connection is
# impossible regardless (can_connect requires the same cohort), so this only governs whether a
# given age group can connect among its own peers. All ages by default — children still need
# active parental consent (can_participate) and any resulting chat is guardian-observable via
# messaging. Note: in prod, minors only exist if minor onboarding is enabled.
CONNECTIONS_ALLOWED_COHORTS = env.list(
    "CONNECTIONS_ALLOWED_COHORTS", default=["adult", "teen", "child"]
)
# Anti-pestering: cap new connection requests per user per window (a repeat request to the same
# person is idempotent and never re-notifies, so this only bounds requests to DISTINCT people).
CONNECTIONS_REQUEST_RATE_LIMIT = env.int("CONNECTIONS_REQUEST_RATE_LIMIT", default=20)
CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS = env.int(
    "CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS", default=3600
)

# --- Communities (derived geo x activity-type discovery labels, e.g. "Cluj-Napoca Football") ---
# A community is materialized PER COHORT only when it clears ALL three floors, counted within
# that cohort over distinct NON-GUARDIAN peers (the k-anon floor keeps a thin bucket from
# pinpointing a minor). Tune per city/cohort. The lookback bounds the nightly GROUP BY.
COMMUNITY_MIN_ACTIVITIES = env.int("COMMUNITY_MIN_ACTIVITIES", default=3)
COMMUNITY_K_ANON_FLOOR = env.int("COMMUNITY_K_ANON_FLOOR", default=5)
COMMUNITY_MIN_DAYS = env.int("COMMUNITY_MIN_DAYS", default=2)
COMMUNITY_LOOKBACK_DAYS = env.int("COMMUNITY_LOOKBACK_DAYS", default=180)
COMMUNITY_ACTIVITIES_PAGE_SIZE = env.int("COMMUNITY_ACTIVITIES_PAGE_SIZE", default=100)

# --- Secure messaging (cohort-safe, invite-accept, end-to-end encrypted) ---
# The server is a zero-knowledge relay: it stores ciphertext + per-recipient wrapped
# keys only. Safety is access-control-based (cohort isolation + invite-accept +
# blocking) plus report-with-decryption — content scanning is impossible under E2EE.
# See docs/MESSAGING.md. These are anti-abuse rate limits (per fixed window).
MESSAGING_RATE_WINDOW_SECONDS = env.int("MESSAGING_RATE_WINDOW_SECONDS", default=60)
MESSAGING_START_RATE_LIMIT = env.int("MESSAGING_START_RATE_LIMIT", default=20)
MESSAGING_SEND_RATE_LIMIT = env.int("MESSAGING_SEND_RATE_LIMIT", default=60)
# Abuse/DoS caps on the E2EE relay: max stored ciphertext per message and max members in
# a group conversation (an unbounded recipient list would amplify per-recipient work).
MESSAGING_MAX_CIPHERTEXT_BYTES = env.int("MESSAGING_MAX_CIPHERTEXT_BYTES", default=65536)
MESSAGING_MAX_GROUP_MEMBERS = env.int("MESSAGING_MAX_GROUP_MEMBERS", default=256)
# Global retention backstop for encrypted messages (0 disables). Per-conversation
# disappearing timers also apply. Purged by the purge_messaging management command.
MESSAGING_RETENTION_DAYS = env.int("MESSAGING_RETENTION_DAYS", default=0)

# --- D9 donations (no ads / no tracking-based monetization) ---
# Pluggable payment provider; default builds an off-platform checkout deep link and
# stores no card data. Swap for a real EU-friendly nonprofit processor in prod.
DONATIONS_PROVIDER = env("DONATIONS_PROVIDER", default="apps.donations.providers.DeepLinkProvider")
DONATIONS_CHECKOUT_BASE_URL = env("DONATIONS_CHECKOUT_BASE_URL", default="")
# Shared secret authenticating provider webhook callbacks (X-Webhook-Secret header).
# The webhook is FAIL-CLOSED: with neither this nor STRIPE_WEBHOOK_SECRET set, every
# callback is rejected, so a pending donation cannot be forged complete.
DONATIONS_WEBHOOK_SECRET = env("DONATIONS_WEBHOOK_SECRET", default="")

# Stripe Checkout (used when DONATIONS_PROVIDER=apps.donations.providers.StripePaymentProvider).
STRIPE_SECRET_KEY = env("STRIPE_SECRET_KEY", default="")
# Stripe webhook signing secret — when set (with the Stripe provider), callbacks are
# authenticated by verifying the Stripe-Signature header instead of the shared secret.
STRIPE_WEBHOOK_SECRET = env("STRIPE_WEBHOOK_SECRET", default="")
DONATIONS_SUCCESS_URL = env("DONATIONS_SUCCESS_URL", default="")
DONATIONS_CANCEL_URL = env("DONATIONS_CANCEL_URL", default="")

# Media object storage (S3-compatible: AWS S3 / Cloudflare R2 / MinIO) for production —
# used when MEDIA_STORAGE_BACKEND=apps.media.storage.S3StorageBackend. Credentials come
# from the environment (AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY).
MEDIA_S3_BUCKET = env("MEDIA_S3_BUCKET", default="")
MEDIA_S3_ENDPOINT_URL = env("MEDIA_S3_ENDPOINT_URL", default="")
MEDIA_S3_REGION = env("MEDIA_S3_REGION", default="")
MEDIA_S3_ADDRESSING_STYLE = env("MEDIA_S3_ADDRESSING_STYLE", default="auto")

# Build/version surfaced by /healthz (set from CI / image tag).
APP_VERSION = env("APP_VERSION", default="0.1.0")
