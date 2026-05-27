import uuid

from django.conf import settings
from django.db import models


class MediaImage(models.Model):
    """An uploaded image — either a profile picture or a photo inside an activity
    thread. There is NO public photo feed: thread photos are visible only to that
    thread's members, profile pictures only to co-members. See docs/ROADMAP.md (D6)."""

    class Kind(models.TextChoices):
        PROFILE = "profile", "Profile picture"
        THREAD_PHOTO = "thread_photo", "Thread photo"

    class Status(models.TextChoices):
        PENDING = "pending", "Pending scan"
        APPROVED = "approved", "Approved"
        REJECTED = "rejected", "Rejected"

    public_id = models.UUIDField(default=uuid.uuid4, editable=False, unique=True)
    owner = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="images"
    )
    kind = models.CharField(max_length=16, choices=Kind.choices)
    # Required for thread photos; null for profile pictures.
    thread = models.ForeignKey(
        "social.Thread",
        null=True,
        blank=True,
        on_delete=models.CASCADE,
        related_name="photos",
    )

    storage_key = models.CharField(max_length=255, blank=True)
    content_type = models.CharField(max_length=32, blank=True)
    byte_size = models.PositiveIntegerField(default=0)
    width = models.PositiveIntegerField(null=True, blank=True)
    height = models.PositiveIntegerField(null=True, blank=True)

    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    scan_reason = models.CharField(max_length=255, blank=True)

    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [
            models.Index(fields=["owner", "kind", "status"]),
            models.Index(fields=["thread", "status"]),
        ]

    def __str__(self):
        return f"{self.kind}({self.owner_id}, {self.status})"
