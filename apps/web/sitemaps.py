"""sitemap.xml — ONLY already-public, open-data pages, for search/AI crawlers.

Runs without ``django.contrib.sites``: ``_BaseSitemap`` supplies the host/scheme from
``SITE_BASE_URL`` (a Sites row would need a migration + admin data we don't want). Every
crawlable URL here is login-free open data:
- static info pages,
- ``public_places()`` venues (the F25 chokepoint — pending/closed venues excluded),
- ``upcoming_events()`` happenings (same gate, narrowed to upcoming).

CHILD-SAFETY INVARIANT: ``social.Activity`` (cohort-scoped, @login_required, may involve
minors) is INTENTIONALLY ABSENT — no Activity sitemap exists, so an activity URL can never
be advertised to a crawler. A test pins this.
"""

from django.conf import settings
from django.contrib.sitemaps import Sitemap
from django.urls import reverse


class _BaseSitemap(Sitemap):
    """Sitemap that resolves its absolute host from SITE_BASE_URL instead of the Sites table."""

    protocol = "https"

    def get_urls(self, page=1, site=None, protocol=None):
        base = (getattr(settings, "SITE_BASE_URL", "") or "").rstrip("/")
        if base:
            scheme, _, host = base.partition("://")
            protocol = protocol or scheme or self.protocol

            class _Host:
                domain = host or base

            site = _Host()
        return super().get_urls(page=page, site=site, protocol=protocol)


class StaticViewSitemap(_BaseSitemap):
    changefreq = "weekly"
    priority = 0.6

    def items(self):
        # URL names of the public, login-free info pages (see apps/web/urls.py).
        return [
            "home",
            "places_list",
            "events_list",
            "things_to_do_index",
            "partners",
            "open_data",
            "transparency",
            "privacy",
            "terms",
        ]

    def location(self, item):
        return reverse(item)


class PlaceSitemap(_BaseSitemap):
    changefreq = "weekly"
    priority = 0.7

    def items(self):
        from apps.places.services import public_places

        return public_places().order_by("pk")

    def location(self, place):
        # Keyword-rich canonical path (place_detail serves the bare /places/<pk>/ form at 200 with
        # this as its canonical <link> — no redirect).
        from .seo import place_path

        return place_path(place)

    def lastmod(self, place):
        return place.last_seen_at


class EventSitemap(_BaseSitemap):
    changefreq = "daily"
    priority = 0.8

    def items(self):
        from apps.events.services import upcoming_events

        return upcoming_events().select_related("place").order_by("starts_at")

    def location(self, event):
        from .seo import event_path

        return event_path(event)

    def lastmod(self, event):
        return event.updated_at


class LandingSitemap(_BaseSitemap):
    """Public city×activity landing pages that currently have real supply (no thin pages)."""

    changefreq = "daily"
    priority = 0.6

    def items(self):
        from .landing import available_landings

        return list(available_landings())

    def location(self, combo):
        area, activity_type = combo
        return reverse("things_to_do", args=[area.slug, activity_type.slug])


SITEMAPS = {
    "static": StaticViewSitemap,
    "places": PlaceSitemap,
    "events": EventSitemap,
    "landing": LandingSitemap,
}
