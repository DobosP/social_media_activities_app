from django.conf import settings
from django.db import models


class Notification(models.Model):
    """An in-app notification. Privacy-respecting: in-app only by default (no email/push,
    no behavioural tracking). External channels can be added later behind opt-in."""

    class Kind(models.TextChoices):
        JOIN_REQUESTED = "join_requested", "Join requested"
        JOIN_APPROVED = "join_approved", "Join approved"
        EVENT_REMINDER = "event_reminder", "Event reminder"
        ACTIVITY_CANCELLED = "activity_cancelled", "Activity cancelled"
        ACTIVITY_UPDATED = "activity_updated", "Activity updated"
        ANNOUNCEMENT = "announcement", "Organizer announcement"
        MODERATION = "moderation", "Moderation notice"
        SYSTEM = "system", "System"

    recipient = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="notifications"
    )
    kind = models.CharField(max_length=24, choices=Kind.choices, default=Kind.SYSTEM)
    title = models.CharField(max_length=200)
    body = models.TextField(blank=True)
    # An in-app link (e.g. /api/social/activities/12/) — never an external tracker.
    url = models.CharField(max_length=300, blank=True)
    read_at = models.DateTimeField(null=True, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["recipient", "read_at"])]

    def __str__(self):
        return f"{self.kind} -> {self.recipient_id}"
