from django.conf import settings
from django.contrib.postgres.fields import ArrayField
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
        ARRIVAL = "arrival", "Arrival"
        CONNECTION_REQUEST = "connection_request", "Connection request"
        CONNECTION_ACCEPTED = "connection_accepted", "Connection accepted"
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


# DSA-mandated notices that may NEVER be muted: MODERATION carries Art.17 statements of
# reasons, SYSTEM carries Art.16 report acknowledgements. Single source of truth; checked
# FIRST in notify() so a stale/crafted muted_kinds row can never suppress them.
NON_MUTABLE_KINDS = frozenset({Notification.Kind.MODERATION, Notification.Kind.SYSTEM})
# Everything else may be muted by the user, in display order.
MUTABLE_KINDS = [k for k in Notification.Kind if k not in NON_MUTABLE_KINDS]

# A short, honest "why you got this" line per kind (text-first; no behavioural data).
WHY_REASONS = {
    Notification.Kind.JOIN_REQUESTED: "Someone asked to join an activity you organise.",
    Notification.Kind.JOIN_APPROVED: "You were admitted to an activity.",
    Notification.Kind.EVENT_REMINDER: "An activity you joined is starting soon.",
    Notification.Kind.ACTIVITY_CANCELLED: "An activity you joined was cancelled.",
    Notification.Kind.ACTIVITY_UPDATED: "An activity you joined changed.",
    Notification.Kind.ANNOUNCEMENT: (
        "An organiser posted an announcement in an activity you joined."
    ),
    Notification.Kind.ARRIVAL: "Someone arrived at your meetup (or your ward arrived).",
    Notification.Kind.CONNECTION_REQUEST: (
        "Someone you've shared an activity with asked to connect."
    ),
    Notification.Kind.CONNECTION_ACCEPTED: "Someone accepted your connection request.",
    Notification.Kind.MODERATION: (
        "A moderation decision affected your content or account (you cannot turn these off)."
    ),
    Notification.Kind.SYSTEM: (
        "An important account or safety notice (you cannot turn these off)."
    ),
}


class NotificationPreference(models.Model):
    """Per-user notification settings. One row per user (user is the PK, so a check is a
    single indexed lookup). ``muted_kinds`` holds the Kind values the user has silenced;
    the DSA non-mutable kinds are refused at write time and again at the notify() gate."""

    user = models.OneToOneField(
        settings.AUTH_USER_MODEL,
        on_delete=models.CASCADE,
        related_name="notification_preference",
        primary_key=True,
    )
    muted_kinds = ArrayField(models.CharField(max_length=24), default=list, blank=True)

    def __str__(self):
        return f"prefs for {self.user_id}"
