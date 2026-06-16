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
        GROUP_ANNOUNCEMENT = "group_announcement", "Group announcement"
        MEETUP_CONFIRMED = "meetup_confirmed", "Meetup confirmed"
        ARRIVAL = "arrival", "Arrival"
        CONNECTION_REQUEST = "connection_request", "Connection request"
        CONNECTION_ACCEPTED = "connection_accepted", "Connection accepted"
        MENTION = "mention", "Mention"
        ORGANIZER_ROLE = "organizer_role", "Organizer role changed"
        ACTIVITY_MATCH = "activity_match", "Saved-search match"  # F3 (mutable, opt-in)
        # W3-F9 (mutable, opt-in, at-most-once per gauge) — the gauge-lane sibling of ACTIVITY_MATCH
        GAUGE_MATCH = "gauge_match", "Saved-search gauge match"
        GROUP_QUESTION = "group_question", "Group question"  # F30 (mutable, organiser-only)
        INTEREST_CONVERTED = "interest_converted", "Gauge converted"  # F27 (mutable)
        RSVP_NUDGE = "rsvp_nudge", "RSVP nudge"  # W2-F11 (mutable, at-most-once, self only)
        # W3-F6 (mutable, at-most-once, organiser self only — never a member fan-out)
        ORGANIZER_PREP = "organizer_prep", "Organizer prep reminder"
        # W3-F7 (mutable, at-most-once per (guardian, activity)) — to a CHILD organiser's ACTIVE
        # guardians when their child's supervised meetup is stuck for lack of a seated supervisor.
        SUPERVISOR_NEEDED = "supervisor_needed", "Supervisor needed"
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
    Notification.Kind.GROUP_ANNOUNCEMENT: (
        "An organiser posted an announcement in a group you're a member of."
    ),
    Notification.Kind.MEETUP_CONFIRMED: (
        "A meetup you joined reached the organiser's minimum number of people going."
    ),
    Notification.Kind.ARRIVAL: (
        "Someone arrived at or is on their way to a meetup you're part of, "
        "or that someone you look after joined."
    ),
    Notification.Kind.CONNECTION_REQUEST: (
        "Someone you've shared an activity with asked to connect."
    ),
    Notification.Kind.CONNECTION_ACCEPTED: "Someone accepted your connection request.",
    Notification.Kind.MENTION: (
        "Someone @mentioned you in an activity thread and chose to notify you "
        "(you can turn these off)."
    ),
    Notification.Kind.ORGANIZER_ROLE: (
        "An organiser of an activity made you a co-organiser, removed that role, or "
        "handed the activity over to you."
    ),
    Notification.Kind.ACTIVITY_MATCH: (
        "A new activity matched a search you saved (you can turn these off)."
    ),
    Notification.Kind.GAUGE_MATCH: (
        "A new interest gauge matched a search you saved (you can turn these off)."
    ),
    Notification.Kind.GROUP_QUESTION: (
        "A member of an under-18 group you organise sent you one of a fixed set of "
        "questions (you can turn these off)."
    ),
    Notification.Kind.INTEREST_CONVERTED: (
        "A meetup you signalled interest in became a real activity (you can turn these off)."
    ),
    Notification.Kind.RSVP_NUDGE: (
        "A meetup you joined is coming up and you haven't said whether you're coming "
        "(you can turn these off)."
    ),
    Notification.Kind.ORGANIZER_PREP: (
        "A meetup you organise is coming up and still has no meeting point "
        "(you can turn these off)."
    ),
    Notification.Kind.SUPERVISOR_NEEDED: (
        "A child you look after is organising a meetup that needs an adult to supervise "
        "(you can turn these off)."
    ),
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
