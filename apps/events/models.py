from django.contrib.postgres.indexes import GinIndex
from django.db import models
from django.db.models import Q, UniqueConstraint

from apps.safety.sanitize import safe_external_url


class Event(models.Model):
    """A happening at a place — pulled from venue calendars (iCal feeds), Google, or
    user/manual entry. Associating events with collected places answers "is something
    happening there" (ROADMAP D7), complementing the static place data."""

    class Source(models.TextChoices):
        ICAL = "ical", "iCalendar feed"
        GOOGLE = "google", "Google"
        USER = "user", "User-submitted"
        MANUAL = "manual", "Manual"

    place = models.ForeignKey(
        "places.Place",
        on_delete=models.CASCADE,
        null=True,
        blank=True,
        related_name="events",
    )
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="events",
    )
    title = models.CharField(max_length=255)
    description = models.TextField(blank=True)
    starts_at = models.DateTimeField()
    ends_at = models.DateTimeField(null=True, blank=True)
    url = models.URLField(max_length=500, blank=True)

    source = models.CharField(max_length=16, choices=Source.choices, default=Source.MANUAL)
    external_id = models.CharField(max_length=200, blank=True)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            UniqueConstraint(
                fields=["source", "external_id"],
                condition=~Q(external_id=""),
                name="uq_event_source_external",
            ),
        ]
        indexes = [
            models.Index(fields=["place", "starts_at"]),
            models.Index(fields=["starts_at"]),
            # W1 search: trigram GIN so the events search (icontains) stays index-assisted.
            GinIndex(name="event_title_trgm", fields=["title"], opclasses=["gin_trgm_ops"]),
            GinIndex(name="event_desc_trgm", fields=["description"], opclasses=["gin_trgm_ops"]),
        ]
        ordering = ["starts_at"]

    def __str__(self):
        return f"{self.title} @ {self.starts_at:%Y-%m-%d %H:%M}"

    def save(self, *args, **kwargs):
        # Untrusted feed URL served raw over the API — persist only safe http(s) links.
        self.url = safe_external_url(self.url)
        super().save(*args, **kwargs)


class EventFeed(models.Model):
    """W9 multi-source aggregation prep: an operator-registered external calendar the
    nightly ``sync_event_feeds`` job pulls. One row per source feed; events upsert
    idempotently with a per-feed-namespaced external id ("feed<pk>:<uid>") so two
    providers reusing the same UID can never collide or overwrite each other.

    Staff-curated (operator/admin) — never user-submitted — so the SSRF posture of the
    fetcher only ever sees vetted URLs. New API-based sources plug in the same way:
    register an adapter (INGESTION_EXTRA_ADAPTERS) or add a feed row here."""

    name = models.CharField(max_length=120)
    url = models.URLField(max_length=500)
    # Optional default bindings applied to every event in this feed.
    place = models.ForeignKey(
        "places.Place", on_delete=models.SET_NULL, null=True, blank=True, related_name="feeds"
    )
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="event_feeds",
    )
    is_active = models.BooleanField(default=True)
    last_synced_at = models.DateTimeField(null=True, blank=True)
    # Truncated last outcome ("ok: 12 events" / "error: …") for the ops page/admin.
    last_status = models.CharField(max_length=200, blank=True, default="")
    created_at = models.DateTimeField(auto_now_add=True)

    def __str__(self):
        return f"{self.name} ({'active' if self.is_active else 'inactive'})"
