"""URL sanitization shared across apps.

Place/event URLs come from untrusted sources (OSM, iCal feeds, enrichers) and are
rendered into `href`/`src` attributes. A `javascript:`/`data:`/`vbscript:` URL there is
stored XSS. Model `.save()` does not run field validators, so untrusted URLs are
sanitized at every write path AND defensively at render time via the `safe_href` filter.
"""

_ALLOWED_SCHEMES = ("http://", "https://")


def safe_external_url(value: str) -> str:
    """Return `value` if it is a safe absolute http(s) URL, else "". Blocks script-bearing
    schemes (javascript:, data:, vbscript:, ...) and protocol-relative `//host` URLs."""
    if not value:
        return ""
    candidate = str(value).strip()
    return candidate if candidate.lower().startswith(_ALLOWED_SCHEMES) else ""


def safe_href(value: str) -> str:
    """Like `safe_external_url`, but also permits site-relative internal links
    (`/path`, not `//host`) — e.g. notification deep-links. For rendering hrefs."""
    if not value:
        return ""
    candidate = str(value).strip()
    if candidate.startswith("/") and not candidate.startswith("//"):
        return candidate
    return safe_external_url(candidate)
