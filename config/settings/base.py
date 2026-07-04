"""Base settings shared across environments."""

from pathlib import Path

import environ
from csp.constants import NONCE

BASE_DIR = Path(__file__).resolve().parent.parent.parent

env = environ.Env(
    DJANGO_DEBUG=(bool, False),
)
# Local dev reads a .env file if present; Docker/CI pass env vars directly.
environ.Env.read_env(BASE_DIR / ".env")

SECRET_KEY = env("DJANGO_SECRET_KEY", default="insecure-dev-key-change-me")
DEBUG = env.bool("DJANGO_DEBUG", default=False)
ALLOWED_HOSTS = env.list("DJANGO_ALLOWED_HOSTS", default=[])

# Absolute base URL (scheme + host, no trailing slash) used to build canonical links,
# the sitemap, and JSON-LD structured data for AI-agent / search discoverability. Leave
# empty in dev (URLs are then derived from the request); prod derives it from
# RENDER_EXTERNAL_HOSTNAME when unset (see prod.py). Set it to a custom domain
# (e.g. "https://meet.example.eu") to flip every absolute URL with one env var.
SITE_BASE_URL = env("SITE_BASE_URL", default="").rstrip("/")
# Display name used in <title>/OpenGraph; kept in settings so it is not hard-coded per page.
SITE_NAME = env("SITE_NAME", default="Activities")

# IndexNow (Bing/Yandex instant indexing) — fully opt-in. Disabled by default so dev/CI make no
# outbound calls; set both to push recently-changed PUBLIC URLs from the run_due_jobs tick. The key
# is also served verbatim at /indexnow.txt for the keyLocation handshake.
INDEXNOW_ENABLED = env.bool("INDEXNOW_ENABLED", default=False)
INDEXNOW_KEY = env("INDEXNOW_KEY", default="")

# Search-engine ownership verification tokens (rendered as <meta> only when set) — let an
# operator verify Google Search Console / Bing Webmaster Tools without DNS or a file upload.
GOOGLE_SITE_VERIFICATION = env("GOOGLE_SITE_VERIFICATION", default="")
BING_SITE_VERIFICATION = env("BING_SITE_VERIFICATION", default="")

# Entity enrichment for the home-page Organization JSON-LD (helps Google Knowledge Graph + LLMs
# resolve the org as one entity). Empty by default → nothing extra emitted.
SITE_SAMEAS = env.list("SITE_SAMEAS", default=[])  # official URLs (repo, socials)
SITE_CONTACT_EMAIL = env("SITE_CONTACT_EMAIL", default="")
SITE_AREA_SERVED = env("SITE_AREA_SERVED", default="Cluj-Napoca")

INSTALLED_APPS = [
    # daphne must precede staticfiles so its ASGI runserver takes over (D5 chat).
    "daphne",
    "django.contrib.admin",
    "django.contrib.auth",
    "django.contrib.contenttypes",
    "django.contrib.sessions",
    "django.contrib.messages",
    "django.contrib.staticfiles",
    # Ships no migrations and does NOT require django.contrib.sites — we supply the host
    # from SITE_BASE_URL via a shim base class (apps/web/sitemaps.py), so adding this keeps
    # `makemigrations --check` green while exposing a public sitemap for crawlers.
    "django.contrib.sitemaps",
    # Required for OpClass to be registered as an index-expression wrapper (the W1
    # trigram expression indexes) — without the app's ready() hook, Django parenthesizes
    # the opclass into the expression and emits invalid SQL.
    "django.contrib.postgres",
    "django.contrib.gis",
    # Third-party
    "rest_framework",
    "rest_framework.authtoken",  # W10: opaque API tokens for native clients
    "rest_framework_gis",
    "django_filters",
    "drf_spectacular",
    "channels",
    "django_prometheus",  # P1 observability: request metrics (exposed via the token-gated /metrics)
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
    "apps.saved_searches",
    "apps.web",
]

MIDDLEWARE = [
    # SecurityMiddleware stays at index 0 (prod inserts WhiteNoise right after it). Prometheus
    # request metrics then wrap the rest of the stack: Before early, After last.
    "django.middleware.security.SecurityMiddleware",
    "django_prometheus.middleware.PrometheusBeforeMiddleware",
    # Assign an X-Request-ID early so it tags every log line + the response + the Sentry scope.
    "apps.ops.middleware.RequestIDMiddleware",
    # PII-safe request completion logs, carrying the request id from the middleware above.
    "apps.ops.middleware.RequestLogMiddleware",
    "apps.ops.middleware.PermissionsPolicyMiddleware",
    # Content-Security-Policy (report-only by default — see CONTENT_SECURITY_POLICY_REPORT_ONLY).
    "csp.middleware.CSPMiddleware",
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
    "django_prometheus.middleware.PrometheusAfterMiddleware",
]

ROOT_URLCONF = "config.urls"

# Browser security headers (ADR-0015). SecurityMiddleware emits the first three headers for every
# response; Permissions-Policy is emitted by apps.ops.middleware.PermissionsPolicyMiddleware because
# Django does not provide a built-in setting for it. Geolocation stays same-origin for request-only
# proximity flows; camera/microphone and other powerful browser features are disabled.
SECURE_CONTENT_TYPE_NOSNIFF = True
SECURE_REFERRER_POLICY = "same-origin"
SECURE_CROSS_ORIGIN_OPENER_POLICY = "same-origin"
PERMISSIONS_POLICY = (
    "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()"
)

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
                "apps.web.context_processors.display_preferences",
                # Canonical URL + site name + OpenGraph defaults for every page <head>.
                "apps.web.context_processors.seo",
            ],
        },
    },
]

WSGI_APPLICATION = "config.wsgi.application"
ASGI_APPLICATION = "config.asgi.application"

# Content-Security-Policy (P1 hardening). Report-only remains the default so production can collect
# browser reports before breaking UI. The server-rendered web UI now loads executable page scripts
# from static files, nonces the remaining JSON script islands, and keeps key SSR pages free of
# inline style attributes/blocks. Flip DJANGO_CSP_ENFORCE=True only after reviewing collected
# reports from /api/v1/ops/csp-report/ for the deployed templates/assets.
_CONTENT_SECURITY_POLICY = {
    "DIRECTIVES": {
        "default-src": ["'self'"],
        "script-src": ["'self'", "https://unpkg.com", NONCE],
        "style-src": ["'self'", "https://unpkg.com"],
        "img-src": ["'self'", "data:", "https://*.tile.openstreetmap.org", "https://unpkg.com"],
        # Same-origin fetch/XHR plus same-host WebSockets for activity chat and E2EE messaging.
        # Some browsers do not treat 'self' as covering ws:// / wss://, so keep schemes explicit.
        "connect-src": ["'self'", "ws:", "wss:"],
        "font-src": ["'self'"],
        "worker-src": ["'self'"],
        "object-src": ["'none'"],
        "base-uri": ["'self'"],
        "frame-ancestors": ["'none'"],
        "form-action": ["'self'"],
        # Collect violations: report-uri (works in all current browsers) + report-to (modern,
        # references the "csp" group in the Reporting-Endpoints header set by the ops middleware).
        # The endpoint sanitizes, logs, and 204s — see apps.ops.views.CSPReportView
        # (apps.ops.urls is mounted under /api/v1/, so the collector lives at
        # /api/v1/ops/csp-report/).
        "report-uri": ["/api/v1/ops/csp-report/"],
        "report-to": "csp",
    },
}
CSP_ENFORCE = env.bool("DJANGO_CSP_ENFORCE", default=False)
if CSP_ENFORCE:
    CONTENT_SECURITY_POLICY = _CONTENT_SECURITY_POLICY
else:
    CONTENT_SECURITY_POLICY_REPORT_ONLY = _CONTENT_SECURITY_POLICY
# Reporting-API endpoint group name -> URL, emitted as the Reporting-Endpoints header by
# apps.ops.middleware.PermissionsPolicyMiddleware so the report-to directive above resolves.
CSP_REPORTING_ENDPOINTS = {"csp": "/api/v1/ops/csp-report/"}

# Prometheus /metrics is gated on a bearer token — CLOSED BY DEFAULT (empty token => 403), never
# world-readable. Set METRICS_TOKEN and have the scraper send `Authorization: Bearer <token>`.
METRICS_TOKEN = env("METRICS_TOKEN", default="")

# Structured logging + request correlation (P1). LOG_FORMAT=json (prod) emits one JSON line per
# record with the X-Request-ID; "plain" (dev/test default) is human-readable. apps.* log at
# LOG_LEVEL (default INFO — joins, moderation, retention); noisy libraries stay at WARNING.
_LOG_FORMAT = env("LOG_FORMAT", default="plain")
REQUEST_LOGGING_ENABLED = env.bool("REQUEST_LOGGING_ENABLED", default=not DEBUG)
LOGGING = {
    "version": 1,
    "disable_existing_loggers": False,
    "filters": {"request_id": {"()": "apps.ops.observability.RequestIdFilter"}},
    "formatters": {
        "plain": {"format": "%(asctime)s %(levelname)s %(name)s [%(request_id)s] %(message)s"},
        "json": {"()": "apps.ops.observability.JsonFormatter"},
    },
    "handlers": {
        "console": {
            "class": "logging.StreamHandler",
            "filters": ["request_id"],
            "formatter": "json" if _LOG_FORMAT == "json" else "plain",
        },
    },
    "root": {"handlers": ["console"], "level": "WARNING"},
    "loggers": {
        "apps": {
            "handlers": ["console"],
            "level": env("LOG_LEVEL", default="INFO"),
            "propagate": False,
        },
    },
}

# Cron heartbeat (dead-man's-switch): run_due_jobs pings this URL on a fully-successful run, so a
# missed/failed nightly pass (GDPR retention / DSA suspension-lifts) raises an alert. "" disables.
OPS_HEARTBEAT_URL = env("OPS_HEARTBEAT_URL", default="")

# Durable off-request task foundation (apps.ops.tasks; drained by the process_deferred_tasks job
# inside run_due_jobs). Postgres-backed, no broker — see docs/ASYNC_TASKS.md.
DEFERRED_TASKS_BATCH = env.int("DEFERRED_TASKS_BATCH", default=100)  # max tasks per drain pass
DEFERRED_TASKS_MAX_ATTEMPTS = env.int("DEFERRED_TASKS_MAX_ATTEMPTS", default=5)  # then FAILED
DEFERRED_TASKS_BACKOFF_BASE = env.int("DEFERRED_TASKS_BACKOFF_BASE", default=30)  # seconds
DEFERRED_TASKS_MAX_BACKOFF = env.int("DEFERRED_TASKS_MAX_BACKOFF", default=3600)  # seconds (cap)

# Postgres + PostGIS. django-environ maps the `postgis://` scheme to the
# GeoDjango backend (django.contrib.gis.db.backends.postgis).
DATABASES = {
    "default": env.db(
        "DATABASE_URL",
        default="postgis://app:app@localhost:5432/app",
    ),
}
# P1 scale: when behind a transaction-pooling PgBouncer, server-side cursors break and persistent
# server connections are counter-productive. Inert by default; set DB_POOLED=True (and
# DB_CONN_MAX_AGE=0 in prod) once a pooler fronts Postgres. See docs/PRODUCTION_READINESS.md.
if env.bool("DB_POOLED", default=False):
    DATABASES["default"]["DISABLE_SERVER_SIDE_CURSORS"] = True
    DATABASES["default"]["CONN_MAX_AGE"] = 0

# Storage hygiene for the in-app notification table (read, non-DSA notices are purged past this by
# the purge_read_notifications job; 0 disables). MODERATION/SYSTEM DSA notices are never purged.
NOTIFICATION_RETENTION_DAYS = env.int("NOTIFICATION_RETENTION_DAYS", default=180)
NOTIFICATION_RETENTION_BATCH = env.int("NOTIFICATION_RETENTION_BATCH", default=1000)

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

# One real person = one account. When a wallet presentation proves possession of the holder
# key (holder_proof == "verified"), bind_identity() records an HMAC of the credential subject
# so the same wallet can never assure two accounts. The HMAC key keeps the stored hash
# unlinkable to the raw subject; the raw subject itself is NEVER stored. Enforcement is OFF by
# default so the dev/sandbox flow (no key-binding proof) is unaffected; turn it on once real
# EUDI wallets present proofs in production.
IDENTITY_UNIQUENESS_ENFORCED = env.bool("IDENTITY_UNIQUENESS_ENFORCED", default=False)
# Defaults to SECRET_KEY for dev/test convenience. In production with uniqueness ENFORCED this
# default is rejected (see config/settings/prod.py): it must be a dedicated, stable secret, because
# rotating SECRET_KEY would otherwise change every holder_hash and break the ban-evasion ledger.
IDENTITY_BINDING_SECRET = env("IDENTITY_BINDING_SECRET", default=SECRET_KEY)

# Phase 4 self-progression: the evolving avatar reflects a user's OWN confirmed real meetups and is
# shown only on their own surfaces. When False (default), other people always see the BASE avatar —
# zero observable progression signal to anyone else (no leaderboard, no cross-user comparison). Turn
# on only if a deployment deliberately wants progression visible to peers.
PROGRESSION_AVATAR_PUBLIC = env.bool("PROGRESSION_AVATAR_PUBLIC", default=False)

# Whether minors can be onboarded (guardian-linked + consented) on this deployment.
# The current guardian-link flow establishes a relationship on mutual confirmation but does
# NOT cryptographically prove a real parent-child / legal-guardianship relationship, and no
# production-grade age/parental-responsibility trust anchor exists yet (EUDI wallets not live;
# the EU age-verification app was bypassed). So this is ON for dev/test but prod.py defaults
# it OFF — a prod deploy runs adults-only until a real trust anchor (EUDI guardian flow /
# national eID / blessed out-of-band process) is wired. See docs/archive/AUDIT_STRESS_2026-05-29.md.
ALLOW_MINOR_ONBOARDING = env.bool("ALLOW_MINOR_ONBOARDING", default=True)

# F6 re-verify-or-pause sweep: how many days before an age proof lapses to nudge re-verification,
# and a hard per-tick cap on EVICTIONS (a clock-skew / mass-expiry event above this is audited and
# capped rather than silently evicting a whole cohort in one run).
REVERIFY_REMINDER_DAYS = env.int("REVERIFY_REMINDER_DAYS", default=14)
REVERIFY_SWEEP_BATCH = env.int("REVERIFY_SWEEP_BATCH", default=1000)

# W3-F4 parental-consent renewal sweep (mirrors the reverify sweep above): the default validity a
# newly granted/renewed consent gets, how many days before it lapses to nudge the ACTIVE guardians,
# and a hard per-tick cap on EVICTIONS (audited if exceeded). Consents granted before W3-F4 have no
# expiry and are grandfathered (never lapse) until they are next renewed.
CONSENT_VALIDITY_DAYS = env.int("CONSENT_VALIDITY_DAYS", default=365)
CONSENT_RENEWAL_REMINDER_DAYS = env.int("CONSENT_RENEWAL_REMINDER_DAYS", default=14)
CONSENT_SWEEP_BATCH = env.int("CONSENT_SWEEP_BATCH", default=1000)

# F4 recurring activity series: how many days ahead the nightly spawn job materialises the next
# instance (so members can discover/join before it starts), and a hard per-tick cap on spawns (a
# clock-skew / backlog event above this is capped rather than mass-creating activities in one run).
SERIES_SPAWN_LEAD_DAYS = env.int("SERIES_SPAWN_LEAD_DAYS", default=14)
SERIES_SPAWN_BATCH = env.int("SERIES_SPAWN_BATCH", default=500)

# F3 saved-search alerts: anti-abuse on creation (rate + hard cap per user), a per-tick match batch
# cap, and a per-saver notify cap so one viral activity can never flood a single user.
SAVED_SEARCH_RATE_LIMIT = env.int("SAVED_SEARCH_RATE_LIMIT", default=20)
SAVED_SEARCH_RATE_WINDOW_SECONDS = env.int("SAVED_SEARCH_RATE_WINDOW_SECONDS", default=3600)
SAVED_SEARCH_MAX_PER_USER = env.int("SAVED_SEARCH_MAX_PER_USER", default=20)
SAVED_SEARCH_MATCH_BATCH = env.int("SAVED_SEARCH_MATCH_BATCH", default=1000)
SAVED_SEARCH_NOTIFY_RATE_LIMIT = env.int("SAVED_SEARCH_NOTIFY_RATE_LIMIT", default=50)
SAVED_SEARCH_NOTIFY_WINDOW_SECONDS = env.int("SAVED_SEARCH_NOTIFY_WINDOW_SECONDS", default=86400)

# Guardianship link invites (verified-adult → minor, mutually confirmed). How long an
# unaccepted invite stays valid, and anti-abuse limits on issuing invites.
GUARDIAN_INVITE_TTL_DAYS = env.int("GUARDIAN_INVITE_TTL_DAYS", default=7)
GUARDIAN_INVITE_RATE_LIMIT = env.int("GUARDIAN_INVITE_RATE_LIMIT", default=20)
GUARDIAN_INVITE_RATE_WINDOW_SECONDS = env.int("GUARDIAN_INVITE_RATE_WINDOW_SECONDS", default=3600)
# F7: throttle guardrail edits (each writes an audit row) — mirrors the guardian-invite throttle.
GUARDIAN_GUARDRAIL_RATE_LIMIT = env.int("GUARDIAN_GUARDRAIL_RATE_LIMIT", default=30)
GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS = env.int(
    "GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS", default=3600
)
# F9: require CHILD-cohort meetups to be at a known public venue type (a staff-curated
# ChildVenueClass) or a staff-approved place. Default ON — it's a child-safety gate.
CHILD_PUBLIC_VENUES_ONLY = env.bool("CHILD_PUBLIC_VENUES_ONLY", default=True)

# Number of trusted reverse proxies in front of the app (e.g. Render's edge = 1). Used for
# both DRF throttle identity AND the web login-lockout's real-client-IP derivation, so they
# agree on which X-Forwarded-For hop to trust (a spoofed XFF must not mint a fresh bucket).
NUM_PROXIES = env.int("NUM_PROXIES", default=1)

REST_FRAMEWORK = {
    "DEFAULT_SCHEMA_CLASS": "drf_spectacular.openapi.AutoSchema",
    # W10 mobile-readiness: session auth for the web UI + opaque DRF tokens for native
    # clients / service accounts (no JWT — tokens are server-validated, instantly
    # revocable, and carry no PII). Obtain at /api/auth/token/ (throttled).
    # Token FIRST: the first authenticator's challenge header decides 401-vs-403 for
    # unauthenticated API calls (Session has none and would force 403s on API clients).
    "DEFAULT_AUTHENTICATION_CLASSES": [
        "rest_framework.authentication.TokenAuthentication",
        "rest_framework.authentication.SessionAuthentication",
    ],
    # Deny-by-default: every endpoint requires auth unless it explicitly opts into AllowAny
    # (the intentionally public ones: places, taxonomy, discovery feeds, donations totals,
    # ops health). This prevents a future viewset from silently inheriting AllowAny.
    "DEFAULT_PERMISSION_CLASSES": [
        "rest_framework.permissions.IsAuthenticated",
    ],
    "DEFAULT_FILTER_BACKENDS": [
        "django_filters.rest_framework.DjangoFilterBackend",
    ],
    "DEFAULT_VERSIONING_CLASS": "rest_framework.versioning.URLPathVersioning",
    "DEFAULT_VERSION": "v1",
    "ALLOWED_VERSIONS": ("v1",),
    # Bounded so a client can't request ?limit=50000 and force a giant unpaginated scan.
    "DEFAULT_PAGINATION_CLASS": "apps.ops.pagination.BoundedLimitOffsetPagination",
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
        # W10: the token-obtain endpoint is a credential-stuffing target — much stricter.
        "token_obtain": env("DRF_THROTTLE_TOKEN_OBTAIN", default="10/min"),
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
    # API versioning: the surface is mounted under /api/v1/ (canonical) + /api/ (alias). Document
    # only the canonical /api/v1/ paths so the schema has one stable, versioned contract.
    "PREPROCESSING_HOOKS": ["config.openapi.only_versioned_endpoints"],
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
# W9 pluggable source registry: {"<source-name>": "dotted.path.AdapterClass"} lets an
# external aggregator add place sources without forking ingest_places (same upsert/
# dedup/overlay-protection semantics). JSON in the env, e.g.
# INGESTION_EXTRA_ADAPTERS='{"eventbrite": "myadapters.EventbritePlaces"}'.
INGESTION_EXTRA_ADAPTERS = env.json("INGESTION_EXTRA_ADAPTERS", default={})

# --- D6 media ---
# Image bytes live in object storage; the local backend is the dev/test default.
MEDIA_STORAGE_BACKEND = env(
    "MEDIA_STORAGE_BACKEND", default="apps.media.storage.LocalStorageBackend"
)
MEDIA_MAX_UPLOAD_BYTES = env.int("MEDIA_MAX_UPLOAD_BYTES", default=5 * 1024 * 1024)
# Longest-side cap; larger uploads are downscaled (privacy + storage/bandwidth).
MEDIA_MAX_DIMENSION = env.int("MEDIA_MAX_DIMENSION", default=2048)
# Smart compression: transcode every uploaded image (photos AND thread attachments) to this codec
# at MEDIA_IMAGE_QUALITY, so private blobs stay small (cheaper EU object storage + less egress).
# WEBP is the recommended default (far smaller than the source PNG/JPEG for a phone photo); set to
# an empty string to preserve the source format. Metadata is stripped + EXIF orientation baked in
# regardless. One upload still = one stored object (no separate thumbnail to manage).
MEDIA_IMAGE_OUTPUT_FORMAT = env("MEDIA_IMAGE_OUTPUT_FORMAT", default="WEBP")
MEDIA_IMAGE_QUALITY = env.int("MEDIA_IMAGE_QUALITY", default=80)
# Decompression-bomb ceiling: reject images whose header-declared pixel count exceeds
# this before any pixels are decoded (≈30 MP default — above real photos, below a bomb).
MEDIA_MAX_IMAGE_PIXELS = env.int("MEDIA_MAX_IMAGE_PIXELS", default=30_000_000)
MEDIA_SIGNED_URL_TTL = env.int("MEDIA_SIGNED_URL_TTL", default=300)
# P1 scale (opt-in): when True AND the storage backend can presign (S3), the media-serving views
# 302-redirect an AUTHORIZED viewer to a short-lived presigned object-store URL instead of streaming
# the bytes through the app process — removing the biggest single-process saturation risk. Default
# False keeps the secure streaming model. CHILD-SAFETY TRADE-OFF: while a minted presigned URL is
# live, a block / moderation-hide / consent or guardian revocation / cohort drift / ephemeral expiry
# does NOT take effect until it expires (the streaming path re-authorizes per byte; the redirect
# does not). MEDIA_PRESIGNED_TTL is therefore DECOUPLED from (much shorter than) the token TTL, to
# keep that revocation window small. PDFs keep forced-download + content-type via the presign
# response overrides. Inert for the local filesystem backend (it can't presign, so it streams).
MEDIA_REDIRECT_TO_PRESIGNED = env.bool("MEDIA_REDIRECT_TO_PRESIGNED", default=False)
# Lifetime of a presigned media redirect URL (seconds). Short by design: it bounds the
# direct-S3 access window during which a revocation/hide/expiry is not yet enforced.
MEDIA_PRESIGNED_TTL = env.int("MEDIA_PRESIGNED_TTL", default=60)
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
# W8 perceptual layer: 64-bit dHash entries (16 hex chars) matched within
# MEDIA_PERCEPTUAL_MAX_DISTANCE bits — catches trivially re-encoded/resized copies of
# known-bad images that defeat exact SHA-256. See apps/media/perceptual.py for limits.
MEDIA_PERCEPTUAL_BLOCKLIST = env.list("MEDIA_PERCEPTUAL_BLOCKLIST", default=[])
MEDIA_PERCEPTUAL_BLOCKLIST_FILE = env("MEDIA_PERCEPTUAL_BLOCKLIST_FILE", default="")
MEDIA_PERCEPTUAL_MAX_DISTANCE = env.int("MEDIA_PERCEPTUAL_MAX_DISTANCE", default=8)
# W8 document (PDF) scanning seam — default no-op; deploy clamd and point this at
# apps.media.docscan.ClamdScanner, then flip the require flag to fail closed.
MEDIA_DOCUMENT_SCANNER = env(
    "MEDIA_DOCUMENT_SCANNER", default="apps.media.docscan.NoopDocumentScanner"
)
MEDIA_REQUIRE_DOCUMENT_SCANNER = env.bool("MEDIA_REQUIRE_DOCUMENT_SCANNER", default=False)
MEDIA_CLAMD_HOST = env("MEDIA_CLAMD_HOST", default="127.0.0.1")
MEDIA_CLAMD_PORT = env.int("MEDIA_CLAMD_PORT", default=3310)
MEDIA_CLAMD_TIMEOUT = env.int("MEDIA_CLAMD_TIMEOUT", default=20)

# W10: API tokens are not forever-credentials — the expire_api_tokens job deletes
# tokens older than this (a mobile client just re-authenticates).
API_TOKEN_MAX_AGE_DAYS = env.int("API_TOKEN_MAX_AGE_DAYS", default=90)

# Thread attachments (images + PDF in the activity conversation). Master switch; per-attachment
# size cap; and the cohorts allowed to share a FILE/PDF (a NEW media type → adults only at
# launch, "none for minors"; images are allowed in any cohort thread).
# INVARIANT: MEDIA_ATTACHMENT_MAX_BYTES + multipart/CSRF overhead must stay UNDER
# MAX_REQUEST_BODY_BYTES (above, default 8 MiB), or the MaxBodySizeMiddleware rejects the upload
# with a raw 413 before the view's friendly "too large" message. 7 MiB leaves ~1 MiB headroom.
MEDIA_ATTACHMENTS_ENABLED = env.bool("MEDIA_ATTACHMENTS_ENABLED", default=True)
MEDIA_ATTACHMENT_MAX_BYTES = env.int("MEDIA_ATTACHMENT_MAX_BYTES", default=7 * 1024 * 1024)
MEDIA_FILE_COHORTS = env.list("MEDIA_FILE_COHORTS", default=["adult"])
# Ephemeral ("temporary") thread pictures. A requested TTL is clamped UP to a per-cohort minimum
# so disappearing media can't be used for "look quick, it's gone" pressure or to outrun a
# guardian/moderator/report. MINORS (child + teen) get a 24h floor; adults a 1h floor. NULL ttl
# (no disappear) stays permanent. The purge job NEVER removes hidden/reported content (evidence).
MEDIA_EPHEMERAL_MIN_TTL_SECONDS = env.int("MEDIA_EPHEMERAL_MIN_TTL_SECONDS", default=3600)
MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS = env.int(
    "MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS", default=86400
)

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
# Default is NudgeMessagePolicy (F33): same trim/cap/empty posture as BasicMessagePolicy, plus a
# SOFT, non-blocking contact-leak signal on ``nudge_hits`` — it never blocks, redacts, or reports,
# so post_to_thread/edit_post behave identically. The user-facing nudge is delivered client-side.
CHAT_MESSAGE_POLICY = env("CHAT_MESSAGE_POLICY", default="apps.chat.policy.NudgeMessagePolicy")
CHAT_MAX_LENGTH = env.int("CHAT_MAX_LENGTH", default=4000)
# Per-user thread-post rate limit (fixed window). Defaults preserve the old chat limits.
# F8 one-tap "I feel unsafe": this web path is otherwise unthrottled. Generous cap (idempotency
# already dedups same-activity re-taps without burning the budget), purely an anti-abuse ceiling.
UNSAFE_REPORT_RATE_LIMIT = env.int("UNSAFE_REPORT_RATE_LIMIT", default=12)
UNSAFE_REPORT_RATE_WINDOW_SECONDS = env.int("UNSAFE_REPORT_RATE_WINDOW_SECONDS", default=3600)
# A panic report stays idempotent while it's still being handled (OPEN/REVIEWING) or was filed
# within this cooldown — so re-taps (and post-resolution mashing) never re-storm the guardians,
# while a genuinely-recurring fear after the cooldown can still raise a fresh alert.
UNSAFE_REPORT_COOLDOWN_SECONDS = env.int("UNSAFE_REPORT_COOLDOWN_SECONDS", default=300)

THREAD_POST_RATE_LIMIT = env.int("THREAD_POST_RATE_LIMIT", default=30)
THREAD_POST_RATE_WINDOW_SECONDS = env.int("THREAD_POST_RATE_WINDOW_SECONDS", default=60)
# Reactions are cheaper than posts (a toggle, not content), so a looser per-user fixed window.
THREAD_REACT_RATE_LIMIT = env.int("THREAD_REACT_RATE_LIMIT", default=60)
THREAD_REACT_RATE_WINDOW_SECONDS = env.int("THREAD_REACT_RATE_WINDOW_SECONDS", default=60)
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

# W4-F30: cohorts whose members may declare a (non-capacity-counted) support-person companion.
# Adults only at launch (mirrors GROUPS_USER_CREATION_COHORTS; connections is no longer an
# adults-only precedent — see docs/adr/0002-cohort-connections-policy.md); a companion is
# structurally never a contact path, so this is defence-in-depth. UNASSIGNED is discarded in code.
SUPPORT_COMPANION_COHORTS = env.list("SUPPORT_COMPANION_COHORTS", default=["adult"])

# --- Communities (derived geo x activity-type discovery labels, e.g. "Cluj-Napoca Football") ---
# A community is materialized PER COHORT only when it clears ALL three floors, counted within
# that cohort over distinct NON-GUARDIAN peers (the k-anon floor keeps a thin bucket from
# pinpointing a minor). Tune per city/cohort. The lookback bounds the nightly GROUP BY.
COMMUNITY_MIN_ACTIVITIES = env.int("COMMUNITY_MIN_ACTIVITIES", default=3)
COMMUNITY_K_ANON_FLOOR = env.int("COMMUNITY_K_ANON_FLOOR", default=5)
COMMUNITY_MIN_DAYS = env.int("COMMUNITY_MIN_DAYS", default=2)
COMMUNITY_LOOKBACK_DAYS = env.int("COMMUNITY_LOOKBACK_DAYS", default=180)
COMMUNITY_ACTIVITIES_PAGE_SIZE = env.int("COMMUNITY_ACTIVITIES_PAGE_SIZE", default=100)

# --- Public Groups (persistent, cohort-pinned, joinable standing groups) ---
# Adult groups are self-creatable only when this is True; default False = staff-curated everywhere
# first (matching the minor-onboarding-off-by-default posture). Minor (CHILD/TEEN) groups are ALWAYS
# staff-curated and additionally gated behind ALLOW_MINOR_ONBOARDING — they ship dark in prod.
GROUPS_ALLOW_USER_CREATED = env.bool("GROUPS_ALLOW_USER_CREATED", default=False)
# Hard-wall: cohorts whose members may SELF-CREATE a group. CHILD/TEEN/UNASSIGNED are discarded
# unconditionally in code (create_group), so a minor can never own a group even by misconfig.
# Adults only at launch. (This wall stands on its own — the CONNECTIONS_ALLOWED_COHORTS hard-wall
# it once mirrored was removed 2026-05-30; see docs/adr/0002-cohort-connections-policy.md.)
GROUPS_USER_CREATION_COHORTS = env.list("GROUPS_USER_CREATION_COHORTS", default=["adult"])
# Anti-spam / anti-reconnaissance rate limits (dedicated buckets, distinct from thread_post).
GROUP_CREATE_RATE_LIMIT = env.int("GROUP_CREATE_RATE_LIMIT", default=5)
GROUP_CREATE_RATE_WINDOW_SECONDS = env.int("GROUP_CREATE_RATE_WINDOW_SECONDS", default=3600)
GROUP_JOIN_RATE_LIMIT = env.int("GROUP_JOIN_RATE_LIMIT", default=20)
GROUP_JOIN_RATE_WINDOW_SECONDS = env.int("GROUP_JOIN_RATE_WINDOW_SECONDS", default=3600)

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
# Server-side encryption at rest. Set to "AES256" for SSE-S3 (provider-managed keys) where the
# bucket/provider supports it (AWS S3, MinIO; many EU S3-compatible providers). Empty = rely on
# the provider's default-at-rest encryption. Objects are private + served only via signed,
# per-viewer, membership-scoped URLs regardless of this setting.
MEDIA_S3_SSE = env("MEDIA_S3_SSE", default="")

# Build/version surfaced by /healthz (set from CI / image tag).
APP_VERSION = env("APP_VERSION", default="0.1.0")
