from django.conf import settings
from django.db import models


class NotificationType(models.TextChoices):
    """Notifications are about the user's OWN activity (no engagement-maxxing, no
    cross-user outreach). Per docs/SAFETY.md nothing here enables adult→minor
    contact: payloads carry the user's own membership/event facts, not other
    users' identities or contact details."""

    JOIN_APPROVED = "join_approved", "Your join request was approved"
    JOIN_REQUESTED = "join_requested", "Someone asked to join your activity"
    EVENT_REMINDER = "event_reminder", "An event you follow is soon"
    ACTIVITY_CANCELLED = "activity_cancelled", "An activity you joined was cancelled"
    SYSTEM = "system", "System message"


# Which types each preference flag gates. Opt-in: a type is delivered only if its
# flag is enabled (defaults below are conservative but on for self-relevant items).
PREFERENCE_FLAGS = {
    "activity_updates": {
        NotificationType.JOIN_APPROVED,
        NotificationType.JOIN_REQUESTED,
        NotificationType.ACTIVITY_CANCELLED,
    },
    "event_reminders": {NotificationType.EVENT_REMINDER},
    "system": {NotificationType.SYSTEM},
}


class NotificationPreference(models.Model):
    """Per-user opt-in switches. Privacy-respecting: defaults deliver only
    self-relevant, non-promotional notifications; everything is user-controllable."""

    user = models.OneToOneField(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="notification_pref"
    )
    activity_updates = models.BooleanField(default=True)
    event_reminders = models.BooleanField(default=True)
    system = models.BooleanField(default=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self):
        return f"notif_pref({self.user_id})"

    def allows(self, ntype: str) -> bool:
        for flag, types in PREFERENCE_FLAGS.items():
            if ntype in types:
                return bool(getattr(self, flag, True))
        return True


class Notification(models.Model):
    """An in-app notification record. Channels (in-app/email/push) deliver it; the
    DB record is the canonical in-app inbox entry."""

    recipient = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="notifications"
    )
    ntype = models.CharField(max_length=32, choices=NotificationType.choices)
    title = models.CharField(max_length=200)
    body = models.CharField(max_length=500, blank=True)
    # Opaque, non-PII context (e.g. {"activity_id": 12}); never another user's PII.
    data = models.JSONField(default=dict, blank=True)
    read_at = models.DateTimeField(null=True, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        ordering = ["-created_at"]
        indexes = [models.Index(fields=["recipient", "read_at"])]

    def __str__(self):
        return f"notification({self.recipient_id}, {self.ntype})"

    @property
    def is_read(self) -> bool:
        return self.read_at is not None
