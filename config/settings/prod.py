"""Production settings. ALLOWED_HOSTS / secrets come from the environment."""

import copy
import os

from django.core.exceptions import ImproperlyConfigured

from .base import *  # noqa: F401,F403
from .base import (
    CACHES,
    CHANNEL_LAYERS,
    DATABASES,
    EUDI_SANDBOX,
    EUDI_TRUSTED_ISSUERS,
    IDENTITY_ALLOW_DEV_PROVIDER,
    IDENTITY_PROVIDER,
    MEDIA_S3_ENDPOINT_URL,
    MEDIA_S3_REGION,
    MEDIA_STORAGE_BACKEND,
    MIDDLEWARE,
    env,
)

DEBUG = False

# Adults-only in production until a real parental-responsibility trust anchor is wired
# (the mutual-click guardian link is NOT verifiable proof of a parent-child relationship).
# Explicitly opt in (ALLOW_MINOR_ONBOARDING=True) only after that — and after the DPIA /
# RO-counsel sign-off. See docs/AUDIT_STRESS_2026-05-29.md (L-GUARDIAN / L-ANCHOR).
ALLOW_MINOR_ONBOARDING = env.bool("ALLOW_MINOR_ONBOARDING", default=False)

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
SECURE_HSTS_SECONDS = env.int("DJANGO_HSTS_SECONDS", default=31536000)  # 1 year
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

# Non-Render EU hosting (e.g. a single Hetzner box behind Caddy/nginx — see docs/HOSTING_EU.md):
# ALLOWED_HOSTS comes from DJANGO_ALLOWED_HOSTS (base.py); CSRF_TRUSTED_ORIGINS had no env hook, so
# an HTTPS form POST from a custom domain failed CSRF. Allow explicit https origins to be supplied.
# Each must include the scheme (Django requires it), e.g. "https://meet.example.eu".
_CSRF_ORIGINS = env.list("DJANGO_CSRF_TRUSTED_ORIGINS", default=[])
if _CSRF_ORIGINS:
    CSRF_TRUSTED_ORIGINS = [*globals().get("CSRF_TRUSTED_ORIGINS", []), *_CSRF_ORIGINS]

# Serve static assets (admin, DRF/Swagger UI) from the app process via WhiteNoise,
# so a single container needs no separate web server or CDN for a demo deploy.
# WhiteNoise must sit immediately after SecurityMiddleware (index 0).
MIDDLEWARE = [MIDDLEWARE[0], "whitenoise.middleware.WhiteNoiseMiddleware", *MIDDLEWARE[1:]]
STORAGES = {
    "default": {"BACKEND": "django.core.files.storage.FileSystemStorage"},
    "staticfiles": {"BACKEND": "whitenoise.storage.CompressedStaticFilesStorage"},
}

# --- Production safety assertions (mirror the SECRET_KEY guard above) ---
# Two tiers. HARD failures (always): the dev identity provider trusts any self-asserted age
# band, and EUDI sandbox trusts a local test issuer — neither may run a minors' platform.
# SOFT (warn unless DJANGO_REQUIRE_SHARED_STATE=True): per-process cache / channel layer are
# fine for a SINGLE-process deploy (the free-tier default) but break global rate-limiting and
# cross-process WebSocket fan-out the moment a 2nd process exists — so we warn loudly and let
# a scaled-out deploy enforce them (set REDIS_URL + DJANGO_REQUIRE_SHARED_STATE=True; enabling
# Redis also requires installing channels-redis — recompile requirements.txt).
#
# These run only when prod is the ACTIVE settings module (server boot, migrate, check,
# collectstatic) — not when prod is imported for inspection while another settings module
# (e.g. config.settings.test) is active, where base.py was already loaded with dev defaults.
if os.environ.get("DJANGO_SETTINGS_MODULE") == "config.settings.prod":
    import warnings

    _require_shared_state = env.bool("DJANGO_REQUIRE_SHARED_STATE", default=False)
    _per_process_problems = []
    if "InMemory" in CHANNEL_LAYERS["default"]["BACKEND"]:
        _per_process_problems.append(
            "InMemoryChannelLayer is per-process and drops cross-process WebSocket messages"
        )
    if "locmem" in CACHES["default"]["BACKEND"].lower():
        _per_process_problems.append(
            "LocMemCache is per-process, so rate limits aren't global and reset on restart"
        )
    if _per_process_problems:
        _msg = (
            "Per-process backends in production: "
            + "; ".join(_per_process_problems)
            + ". Safe only for a SINGLE-process deploy. Set REDIS_URL (and install channels-redis) "
            "before scaling out."
        )
        if _require_shared_state:
            raise ImproperlyConfigured(_msg + " (DJANGO_REQUIRE_SHARED_STATE=True)")
        warnings.warn(_msg, stacklevel=2)
    if IDENTITY_PROVIDER.endswith("dev.DevIdentityProvider") and not IDENTITY_ALLOW_DEV_PROVIDER:
        raise ImproperlyConfigured(
            "Production must use a real IDENTITY_PROVIDER (e.g. the EUDI wallet provider); the "
            "dev provider trusts caller-asserted age bands."
        )
    if EUDI_SANDBOX:
        raise ImproperlyConfigured(
            "EUDI_SANDBOX must be False in production (it trusts a local test issuer that will "
            "sign any claimed age)."
        )
    # A live EUDI provider with no trust anchor verifies nothing (every check fails closed,
    # so no minor can ever be onboarded) with no other signal — require the trust list.
    if "eudi" in IDENTITY_PROVIDER.lower() and not EUDI_TRUSTED_ISSUERS:
        raise ImproperlyConfigured(
            "The EUDI identity provider requires a non-empty EUDI_TRUSTED_ISSUERS trust anchor "
            "in production (the EU trust list / issuer public keys)."
        )
    # EU data residency for minors' media (GDPR Ch. V): if S3-compatible storage is used,
    # require an EU region (or an explicit endpoint, e.g. an EU R2 bucket).
    if MEDIA_STORAGE_BACKEND.endswith("S3StorageBackend") and not (
        MEDIA_S3_REGION.lower().startswith("eu") or MEDIA_S3_ENDPOINT_URL
    ):
        raise ImproperlyConfigured(
            "Media object storage must be in an EU region (set MEDIA_S3_REGION=eu-* or an EU "
            "MEDIA_S3_ENDPOINT_URL) for minors' data residency."
        )

# --- Error tracking (opt-in via SENTRY_DSN; sentry-sdk is only imported when configured) ---
SENTRY_DSN = env("SENTRY_DSN", default="")
if SENTRY_DSN:
    import sentry_sdk
    from sentry_sdk.integrations.django import DjangoIntegration

    sentry_sdk.init(
        dsn=SENTRY_DSN,
        integrations=[DjangoIntegration()],
        # Privacy-first: never attach PII, and don't sample request bodies by default.
        send_default_pii=False,
        traces_sample_rate=env.float("SENTRY_TRACES_SAMPLE_RATE", default=0.0),
        environment=env("SENTRY_ENVIRONMENT", default="production"),
    )
