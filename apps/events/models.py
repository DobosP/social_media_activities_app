from django.conf import settings
from django.contrib.postgres.indexes import GinIndex, OpClass
from django.db import models
from django.db.models import Q, UniqueConstraint
from django.db.models.functions import Upper

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
        SCRAPER = "roedu", "RO-EDU scraper"

    class LifecycleStatus(models.TextChoices):
        """Source-owned event state, separate from member accuracy reports."""

        SCHEDULED = "scheduled", "Scheduled"
        RESCHEDULED = "rescheduled", "Rescheduled"
        POSTPONED = "postponed", "Postponed"
        CANCELLED = "cancelled", "Cancelled"
        SOLD_OUT = "sold_out", "Sold out"
        MOVED_ONLINE = "moved_online", "Moved online"
        EXPIRED = "expired", "Expired"
        REMOVED = "removed", "Removed upstream"
        UNKNOWN = "unknown", "Unknown"

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
    attribution = models.CharField(max_length=255, blank=True)
    license_name = models.CharField(max_length=120, blank=True)
    provenance_url = models.URLField(max_length=500, blank=True)

    # RO-EDU source facts. These remain separate from the app's member-created
    # Activity lifecycle and from EventReport's crowd accuracy overlay.
    source_category = models.CharField(max_length=64, blank=True)
    source_confidence = models.FloatField(null=True, blank=True)
    is_import_held = models.BooleanField(default=False)
    lifecycle_status = models.CharField(
        max_length=24,
        choices=LifecycleStatus.choices,
        default=LifecycleStatus.SCHEDULED,
    )
    is_tombstone = models.BooleanField(default=False)
    source_venue_id = models.CharField(max_length=200, blank=True)
    source_city = models.CharField(max_length=128, blank=True)
    source_pack_id = models.CharField(max_length=255, blank=True)
    source_snapshot_id = models.CharField(max_length=255, blank=True)
    source_release_id = models.CharField(max_length=255, blank=True)
    source_snapshot_generated_at = models.DateTimeField(null=True, blank=True)
    source_first_seen_at = models.DateTimeField(null=True, blank=True)
    source_last_seen_at = models.DateTimeField(null=True, blank=True)
    source_updated_at = models.DateTimeField(null=True, blank=True)
    source_recurrence = models.CharField(max_length=1000, blank=True)
    source_timezone = models.CharField(max_length=64, blank=True)
    source_price_min = models.DecimalField(max_digits=12, decimal_places=2, null=True, blank=True)
    source_price_max = models.DecimalField(max_digits=12, decimal_places=2, null=True, blank=True)
    source_currency = models.CharField(max_length=3, blank=True)
    source_is_free = models.BooleanField(null=True, blank=True)
    source_availability = models.CharField(max_length=16, blank=True)

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
            models.Index(
                fields=["is_tombstone", "is_import_held", "lifecycle_status", "starts_at"],
                name="event_lifecycle_start_idx",
            ),
            models.Index(
                fields=["source", "source_pack_id", "source_city"],
                name="event_roedu_scope_idx",
            ),
            # W1 search: trigram GIN on UPPER(col) — icontains compiles to UPPER() LIKE on
            # Postgres, so only an expression index matches (review finding W1-14).
            GinIndex(OpClass(Upper("title"), name="gin_trgm_ops"), name="event_title_trgm"),
            GinIndex(OpClass(Upper("description"), name="gin_trgm_ops"), name="event_desc_trgm"),
        ]
        ordering = ["starts_at"]

    def __str__(self):
        return f"{self.title} @ {self.starts_at:%Y-%m-%d %H:%M}"

    def save(self, *args, **kwargs):
        # Untrusted feed URL served raw over the API — persist only safe http(s) links.
        self.url = safe_external_url(self.url)
        self.provenance_url = safe_external_url(self.provenance_url)
        super().save(*args, **kwargs)


class RoeduEventSyncState(models.Model):
    """Last completely reconciled immutable RO-EDU event snapshot per scope."""

    pack_id = models.CharField(max_length=255)
    city = models.CharField(max_length=128)
    snapshot_id = models.CharField(max_length=255)
    release_id = models.CharField(max_length=255, blank=True)
    snapshot_generated_at = models.DateTimeField()
    completed_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["pack_id", "city"], name="uq_roedu_event_sync_scope"),
        ]

    def __str__(self):
        return f"{self.pack_id}:{self.city}@{self.snapshot_id}"


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


class EventReport(models.Model):
    """F21: a member's 'this event has changed' report (cancelled / moved / wrong time). A DEDICATED
    ingest-safe overlay — NEVER a field on Event, because upsert_event's update_or_create would
    clobber it on every re-ingest of the feed. 'This may have changed' is derived at READ time from
    a count of RECENT reports (auto-decay: reports outside the window stop counting, so a re-listed
    event self-heals), exactly like F28's OpenNowReport.

    COHORT NOTE (deliberate): events are AllowAny and NOT cohort-scoped, and can_participate is
    cohort-blind — so a verified CHILD and ADULT report into the SAME event tally. That's acceptable
    because an event being cancelled/moved is cohort-neutral physical reality and the tally is
    counts-only (never reporter identity, never a per-user reliability rollup)."""

    class Kind(models.TextChoices):
        CANCELLED = "cancelled", "Cancelled"
        MOVED = "moved", "Moved / wrong place"
        WRONG_TIME = "wrong_time", "Wrong time"

    event = models.ForeignKey(Event, on_delete=models.CASCADE, related_name="reports")
    reporter = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="event_reports"
    )
    kind = models.CharField(max_length=16, choices=Kind.choices)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        # No UniqueConstraint: uniqueness is TEMPORAL (one per reporter per event per decay window),
        # enforced in the service so a post-decay report is allowed again (mirrors OpenNowReport).
        indexes = [
            models.Index(fields=["event", "created_at"]),  # read-time recent-count query
            models.Index(fields=["event", "reporter"]),  # cheap per-window dedup .exists()
        ]

    def __str__(self):
        return f"event_report({self.event_id}.{self.kind} by {self.reporter_id})"
