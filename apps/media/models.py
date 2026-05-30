from django.conf import settings
from django.db import models
from django.db.models import Q, UniqueConstraint


class Photo(models.Model):
    """An uploaded image. The ONLY images in the product: a single profile picture
    per user, or a photo shared privately inside an activity thread. Visibility is
    enforced in services (membership + cohort + scan status); see docs/SAFETY.md."""

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
    width = models.PositiveIntegerField(default=0)
    height = models.PositiveIntegerField(default=0)

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
    lives in the conversation (not a separate gallery). Only ever stored once it has passed the
    same fail-closed scan as a Photo, so there is no PENDING/BLOCKED state — a row existing
    means it is clean. Images are EXIF-stripped + re-encoded; FILE (PDF) is stored as-is and
    only ever served as a forced download (never inline) so it can't execute in the page.
    Visibility = current membership of the post's activity thread (enforced in services)."""

    class Kind(models.TextChoices):
        IMAGE = "image", "Image"
        FILE = "file", "File"

    post = models.ForeignKey("social.Post", on_delete=models.CASCADE, related_name="attachments")
    uploader = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="attachments"
    )
    kind = models.CharField(max_length=8, choices=Kind.choices)
    storage_key = models.CharField(max_length=128)
    content_type = models.CharField(max_length=64)
    byte_size = models.PositiveIntegerField(default=0)
    sha256 = models.CharField(max_length=64, blank=True)
    # Sanitised display name (FILE only); images render without a filename.
    original_filename = models.CharField(max_length=120, blank=True)
    width = models.PositiveIntegerField(default=0)
    height = models.PositiveIntegerField(default=0)
    exif_stripped = models.BooleanField(default=False)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["post", "created_at"])]

    def __str__(self):
        return f"attachment({self.kind}, post={self.post_id})"
