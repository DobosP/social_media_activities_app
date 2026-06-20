"""Absolute-URL helpers for discoverability surfaces (canonical links, sitemap, JSON-LD).

The single seam that turns a relative path into the absolute URL crawlers and answer
engines need. It prefers the configured ``SITE_BASE_URL`` (so pointing a custom domain is
one env var, no code change) and otherwise falls back to the request's own host — correct
behind Render's proxy because ``SECURE_PROXY_SSL_HEADER`` is set, so ``build_absolute_uri``
yields https. No people, no PII here: only paths to already-public pages flow through.
"""

from django.conf import settings


def site_base_url(request=None) -> str:
    """Return the scheme+host base URL (no trailing slash), or '' if it cannot be derived.

    ``SITE_BASE_URL`` wins (custom domain / Render host); otherwise we read it from the
    request. Sitemaps run without a request, so they require ``SITE_BASE_URL`` to be set.
    """
    base = (getattr(settings, "SITE_BASE_URL", "") or "").rstrip("/")
    if base:
        return base
    if request is not None:
        return f"{request.scheme}://{request.get_host()}"
    return ""


def absolute_url(path: str, request=None) -> str:
    """Build an absolute URL for ``path`` (a root-relative path like ``/places/3/``).

    Falls back to the bare path if no base URL is available (dev without SITE_BASE_URL and
    no request) — still valid markup, just not absolute.
    """
    path = path or "/"
    base = site_base_url(request)
    if not base:
        return path
    if not path.startswith("/"):
        path = "/" + path
    return base + path
