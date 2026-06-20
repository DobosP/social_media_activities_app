"""Submit recently-changed PUBLIC URLs to IndexNow (Bing/Yandex) so they re-crawl fast.

Opt-in: a no-op unless IndexNow is enabled + keyed + a SITE_BASE_URL is set
(``indexnow.submit_urls`` short-circuits otherwise). A batch job — not per-save hooks — so the
safety-critical publish paths stay untouched and a bulk OSM re-ingest can't fan out one POST per
row. Submits the slugged URLs of public places (``last_seen_at``) and upcoming events
(``updated_at``) changed within the window, capped per run. Re-submitting is harmless to IndexNow.
"""

from datetime import timedelta

from django.core.management.base import BaseCommand
from django.utils import timezone

DEFAULT_WINDOW_HOURS = 26  # a daily tick + buffer
DEFAULT_MAX_URLS = 1000


class Command(BaseCommand):
    help = "Submit recently-changed public place/event URLs to IndexNow (opt-in, best-effort)."

    def add_arguments(self, parser):
        parser.add_argument("--window-hours", type=int, default=DEFAULT_WINDOW_HOURS)
        parser.add_argument("--max-urls", type=int, default=DEFAULT_MAX_URLS)

    def handle(self, *args, **opts):
        from apps.web.indexnow import is_enabled, submit_urls

        if not is_enabled():
            self.stdout.write("IndexNow disabled — skipping.")
            return

        from apps.events.services import upcoming_events
        from apps.places.services import public_places
        from apps.web.seo import absolute_url, event_path, place_path

        cutoff = timezone.now() - timedelta(hours=opts["window_hours"])
        cap = opts["max_urls"]

        places = public_places().filter(last_seen_at__gte=cutoff).order_by("-last_seen_at")[:cap]
        events = (
            upcoming_events()
            .filter(updated_at__gte=cutoff)
            .select_related("place")
            .order_by("-updated_at")[:cap]
        )
        urls = [absolute_url(place_path(p)) for p in places]
        urls += [absolute_url(event_path(e)) for e in events]

        sent = submit_urls(urls)
        verb = "submitted" if sent else "nothing to submit for"
        self.stdout.write(f"IndexNow: {verb} {len(urls)} URL(s).")
