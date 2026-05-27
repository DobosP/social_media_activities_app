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
