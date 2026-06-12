"""W9: pull every active EventFeed (registered external calendar) and upsert its events.

Runs from ops' run_due_jobs. Per-feed failure isolation (one broken feed never blocks
the rest); external ids are namespaced per feed ("feed<pk>:<uid>") so UID reuse across
providers can never merge two different events."""

from django.core.management.base import BaseCommand
from django.utils import timezone

from apps.events.models import EventFeed
from apps.events.services import import_events
from apps.events.sources import ICalFeedSource


class _NamespacedSource:
    """Wraps an EventSource so every RawEvent's external_id is feed-scoped."""

    def __init__(self, inner, feed_pk: int):
        self.inner = inner
        self.name = inner.name
        self.feed_pk = feed_pk

    def fetch(self):
        for raw in self.inner.fetch():
            if raw.external_id:
                raw.external_id = f"feed{self.feed_pk}:{raw.external_id}"
            yield raw


class Command(BaseCommand):
    help = "Sync all active external event feeds (EventFeed rows) into Events."

    def handle(self, *args, **opts):
        failures = 0
        for feed in EventFeed.objects.filter(is_active=True):
            try:
                source = _NamespacedSource(ICalFeedSource(url=feed.url), feed.pk)
                count = import_events(
                    source, place=feed.place, activity_type=feed.activity_type
                )
                feed.last_status = f"ok: {count} event(s)"
                self.stdout.write(f"{feed.name}: {count} event(s)")
            except Exception as exc:  # noqa: BLE001 — per-feed isolation by design
                failures += 1
                feed.last_status = f"error: {exc}"[:200]
                self.stderr.write(f"{feed.name}: {exc}")
            feed.last_synced_at = timezone.now()
            feed.save(update_fields=["last_synced_at", "last_status"])
        if failures:
            self.stderr.write(self.style.WARNING(f"{failures} feed(s) failed."))
