from django.conf import settings
from django.db import models
from django.db.models import Q, UniqueConstraint


class Photo(models.Model):
    """An uploaded image for a profile or private activity thread.

    Public activity-card cover photos live in ActivityCover below because Photo's
    constraints and visibility semantics are intentionally profile/thread-specific.
    """

    class Kind(models.TextChoices):
        PROFILE = "profile", "Profile picture"
        THREAD = "thread", "Thread photo"

    class ScanStatus(models.TextChoices):
        PENDING = "pending", "Pending"
        CLEAN = "clean", "Clean"
        BLOCKED = "blocked", "Blocked"

    uploader = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="photos"
    )
    kind = models.CharField(max_length=16, choices=Kind.choices)
    thread = models.ForeignKey(
        "social.Thread",
        on_delete=models.CASCADE,
        null=True,
        blank=True,
        related_name="photos",
    )

    storage_key = models.CharField(max_length=128, blank=True)
    content_type = models.CharField(max_length=64, blank=True)
    byte_size = models.PositiveIntegerField(default=0)
    sha256 = models.CharField(max_length=64, blank=True)
    # W8: 64-bit perceptual dHash (16 hex chars) of the stored bytes. Powers near-
    # duplicate profile-picture detection (a resize/re-encode no longer evades the
    # uniqueness rule). Empty = not computed (legacy rows); never treated as a match.
    phash = models.CharField(max_length=16, blank=True, default="")
    width = models.PositiveIntegerField(default=0)
    height = models.PositiveIntegerField(default=0)
    # ADR-0026: one smaller eager rendition served on card/stream surfaces. Empty = none
    # (source already small, or a pre-rendition row) — serving falls back to storage_key.
    thumb_storage_key = models.CharField(max_length=128, blank=True, default="")

    scan_status = models.CharField(
        max_length=16, choices=ScanStatus.choices, default=ScanStatus.PENDING
    )
    exif_stripped = models.BooleanField(default=False)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            # At most one profile picture per user.
            UniqueConstraint(
                fields=["uploader"],
                condition=Q(kind="profile"),
                name="uq_profile_photo_per_user",
            ),
            # Thread photos must reference a thread; profile photos must not.
            models.CheckConstraint(
                condition=(Q(kind="thread") & Q(thread__isnull=False))
                | (Q(kind="profile") & Q(thread__isnull=True)),
                name="photo_thread_matches_kind",
            ),
        ]
        indexes = [models.Index(fields=["thread", "scan_status"])]

    def __str__(self):
        return f"photo({self.kind}, {self.scan_status})"


class Attachment(models.Model):
    """A file shared INSIDE an activity thread, attached to a single ``social.Post`` so media
    lives in the conversation (not a separate gallery). Every kind passes the same fail-closed
    scan BEFORE the row is created. IMAGE/FILE are fully processed in the upload request, so
    for them a row existing means clean AND ready. VIDEO (ADR-0026) additionally goes through
    an asynchronous transcode + frame scan, so it carries a processing ``status`` and is
    WITHHELD (never served, `storage_key` empty) until that flips to READY — deferral never
    moves a safety gate (docs/ASYNC_TASKS.md). Images are EXIF-stripped + re-encoded; FILE
    (PDF) is stored as-is and only ever served as a forced download (never inline) so it can't
    execute in the page; VIDEO is re-encoded to one progressive MP4 (the re-encode IS the
    metadata strip). Visibility = current membership of the post's activity thread (enforced
    in services)."""

    class Kind(models.TextChoices):
        IMAGE = "image", "Image"
        FILE = "file", "File"
        VIDEO = "video", "Video"

    class Status(models.TextChoices):
        # READY is the default so IMAGE/FILE rows (processed synchronously) never transition.
        READY = "ready", "Ready"
        PENDING = "pending", "Pending processing"
        PROCESSING = "processing", "Processing"
        FAILED = "failed", "Failed"
        # Frame scan matched the blocklist: never served (staff evidence only), source retained.
        BLOCKED = "blocked", "Blocked"

    post = models.ForeignKey("social.Post", on_delete=models.CASCADE, related_name="attachments")
    uploader = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="attachments"
    )
    kind = models.CharField(max_length=8, choices=Kind.choices)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.READY)
    storage_key = models.CharField(max_length=128, blank=True, default="")
    content_type = models.CharField(max_length=64)
    byte_size = models.PositiveIntegerField(default=0)
    sha256 = models.CharField(max_length=64, blank=True)
    # Sanitised display name (FILE only); images render without a filename.
    original_filename = models.CharField(max_length=120, blank=True)
    width = models.PositiveIntegerField(default=0)
    height = models.PositiveIntegerField(default=0)
    # ADR-0026 renditions: smaller image rendition (IMAGE) / poster frame (VIDEO).
    thumb_storage_key = models.CharField(max_length=128, blank=True, default="")
    poster_storage_key = models.CharField(max_length=128, blank=True, default="")
    poster_content_type = models.CharField(max_length=64, blank=True, default="")
    # VIDEO lifecycle: the quarantined original awaiting transcode (deleted on success — it
    # still carries the source's metadata), output duration, and worker-claim bookkeeping.
    source_storage_key = models.CharField(max_length=128, blank=True, default="")
    duration_seconds = models.PositiveIntegerField(default=0)
    processing_attempts = models.PositiveSmallIntegerField(default=0)
    processing_started_at = models.DateTimeField(null=True, blank=True)
    exif_stripped = models.BooleanField(default=False)
    created_at = models.DateTimeField(auto_now_add=True)
    # Ephemeral ("temporary picture") support. NULL = permanent (the default). When set, the blob
    # stops being served at expiry and a purge job later reclaims it — UNLESS the post is hidden or
    # under an unresolved report, in which case the evidence is preserved (purge exempts it). A
    # per-cohort minimum TTL (24h for minors) is enforced in the service so disappearing media can
    # never be weaponised for "look quick, it's gone" pressure or to outrun a guardian/report.
    expires_at = models.DateTimeField(null=True, blank=True)
    # Set when the purge job has reclaimed the blob (idempotency + an honest "expired" placeholder).
    # The row is RETAINED after purge (uploader + sha256 + audit survive); only the bytes are gone.
    purged_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        indexes = [
            models.Index(fields=["post", "created_at"]),
            # The purge job scans by (expires_at set, not yet purged) — keep it an index scan.
            models.Index(
                fields=["expires_at"],
                condition=Q(expires_at__isnull=False, purged_at__isnull=True),
                name="ix_attachment_pending_purge",
            ),
            # The transcode worker claims by status — only the (rare, transient) non-ready
            # rows are indexed.
            models.Index(
                fields=["status", "created_at"],
                condition=Q(status__in=["pending", "processing"]),
                name="ix_attachment_needs_work",
            ),
        ]

    def __str__(self):
        return f"attachment({self.kind}, post={self.post_id})"

    def is_available(self, now=None) -> bool:
        """Whether the blob should still be served: ready (video only becomes READY after its
        fail-closed processing), not purged, and not past its expiry. An expired attachment
        stops serving immediately (the moment of expiry), even before the purge runs."""
        from django.utils import timezone

        if self.status != self.Status.READY:
            return False
        if self.purged_at is not None or not self.storage_key:
            return False
        if self.expires_at is None:
            return True
        return self.expires_at > (now or timezone.now())


class ActivityCover(models.Model):
    """One contextual cover photo for a discoverable activity card.

    Visibility is never independent: services re-check the owning activity through
    visible_activities()/public_activities() before issuing or resolving a URL.
    """

    activity = models.OneToOneField(
        "social.Activity", on_delete=models.CASCADE, related_name="cover"
    )
    uploader = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="activity_covers"
    )
    storage_key = models.CharField(max_length=128)
    content_type = models.CharField(max_length=64)
    byte_size = models.PositiveIntegerField(default=0)
    sha256 = models.CharField(max_length=64, blank=True)
    width = models.PositiveIntegerField(default=0)
    height = models.PositiveIntegerField(default=0)
    # ADR-0026: card-sized rendition (discovery cards are the hottest media surface).
    thumb_storage_key = models.CharField(max_length=128, blank=True, default="")
    exif_stripped = models.BooleanField(default=False)
    alt_text = models.CharField(max_length=140, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [models.Index(fields=["activity", "created_at"])]

    def __str__(self):
        return f"activity-cover(activity={self.activity_id})"
