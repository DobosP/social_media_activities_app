"""Absolute-URL helpers for discoverability surfaces (canonical links, sitemap, JSON-LD).

The single seam that turns a relative path into the absolute URL crawlers and answer
engines need. It prefers the configured ``SITE_BASE_URL`` (so pointing a custom domain is
one env var, no code change) and otherwise falls back to the request's own host — correct
behind Render's proxy because ``SECURE_PROXY_SSL_HEADER`` is set, so ``build_absolute_uri``
yields https. No people, no PII here: only paths to already-public pages flow through.
"""

from django.conf import settings
from django.utils.cache import patch_vary_headers
from django.utils.text import slugify


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


def _slug(text: str, fallback: str) -> str:
    """A keyword slug from human text (RO accents handled by slugify), or a fallback."""
    return slugify(text or "") or fallback


PUBLIC_CACHE_SECONDS = 3600  # 1h — anonymous, open-data SEO endpoints; crawl-budget/CDN friendly


def cache_public(response, request=None, max_age: int = PUBLIC_CACHE_SECONDS):
    """Mark an open-data response cacheable — ``public`` for shared caches/CDNs *only* when it
    is safe by construction, else ``private`` (the visitor's own browser, never shared).

    Pass ``request`` for any page rendered from the cookie-bearing base layout (the landing
    pages). Such a page is **never** marked ``public``, because the base template both injects
    per-user nav when authenticated (unread count, guardian/connection flags) *and* mints a
    per-session CSRF cookie + form token for the language switcher even for anonymous visitors
    (the cookie is added by middleware after this returns, so it can't be detected here). A
    shared cache marked ``public`` could replay one visitor's nav — or their ``Set-Cookie:
    csrftoken`` + form token — to everyone hitting the same (cookie-less) cache entry. So:

    * authenticated  -> ``private, no-cache`` (always revalidate; freshest per-user data), and
    * anonymous      -> ``private, max-age`` (the visitor's own browser may reuse it; a shared
      cache must not), plus ``Vary: Cookie`` as defence-in-depth if an intermediary ignores
      ``private``.

    Omit ``request`` only for pure open-data responses with *no* per-user/cookie content at all
    (robots.txt / sitemap.xml / llms.txt) — those alone stay unconditionally ``public``."""
    if request is not None:
        patch_vary_headers(response, ("Cookie",))
        user = getattr(request, "user", None)
        if user is not None and user.is_authenticated:
            response["Cache-Control"] = "private, no-cache"
        else:
            response["Cache-Control"] = f"private, max-age={max_age}"
        return response
    response["Cache-Control"] = f"public, max-age={max_age}"
    return response


def place_path(place) -> str:
    """Canonical, keyword-rich path for a venue: /places/<pk>/<slug>/.

    The slug is derived read-time from display_name (never stored — a crowd correction to the
    name updates the canonical URL on next read). place_detail serves every form (bare
    /places/<pk>/ or a stale slug) at 200 and points here via a canonical <link> — no redirect,
    so a slug change never 301-churns links people have already shared.
    """
    return f"/places/{place.pk}/{_slug(place.display_name, 'place')}/"


def event_path(event) -> str:
    """Canonical, keyword-rich path for an event: /events/<pk>/<slug>/."""
    return f"/events/{event.pk}/{_slug(event.title, 'event')}/"
