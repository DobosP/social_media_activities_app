"""Template filter for safely rendering untrusted URLs into href attributes."""

from django import template

from apps.safety.sanitize import safe_href as _safe_href

register = template.Library()


@register.filter(name="safe_href")
def safe_href(value):
    """Return the URL only if it is a site-relative or http(s) link, else "" — so a
    `javascript:`/`data:` URL from an ingested feed can never execute in an href."""
    return _safe_href(value)
