from django.conf import settings
from django.contrib.contenttypes.fields import GenericForeignKey
from django.contrib.contenttypes.models import ContentType
from django.db import models
from django.db.models import Q, UniqueConstraint
from django.utils import timezone


class ReasonCode(models.TextChoices):
    GROOMING = "grooming", "Grooming / predatory contact"
    HARASSMENT = "harassment", "Harassment / bullying"
    CSAM = "csam", "Child sexual abuse material"
    SPAM = "spam", "Spam"
    OFF_PLATFORM = "off_platform", "Unsafe off-platform / meetup risk"
    OTHER = "other", "Other"


class Report(models.Model):
    """A user report against a user, activity, or post. The moderation review queue
    (Django admin) works these to a resolution."""

    class Status(models.TextChoices):
        OPEN = "open", "Open"
        REVIEWING = "reviewing", "Reviewing"
        ACTIONED = "actioned", "Actioned"
        DISMISSED = "dismissed", "Dismissed"

    reporter = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.SET_NULL, null=True, related_name="reports_made"
    )
    target_type = models.ForeignKey(ContentType, on_delete=models.CASCADE)
    target_id = models.PositiveBigIntegerField()
    target = GenericForeignKey("target_type", "target_id")

    reason = models.CharField(max_length=24, choices=ReasonCode.choices)
    detail = models.TextField(blank=True)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.OPEN)

    handled_by = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="reports_handled",
    )
    handled_at = models.DateTimeField(null=True, blank=True)
    resolution = models.TextField(blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [
            models.Index(fields=["status", "reason"]),
            models.Index(fields=["target_type", "target_id"]),
        ]

    def __str__(self):
        return f"report({self.reason}, {self.status})"


class Block(models.Model):
    """A user blocking another user — suppresses interaction/visibility between them."""

    blocker = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="blocks_made"
    )
    blocked = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="blocked_by"
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["blocker", "blocked"], name="uq_block_pair"),
            models.CheckConstraint(
                condition=~Q(blocker=models.F("blocked")), name="block_not_self"
            ),
        ]

    def __str__(self):
        return f"{self.blocker_id} blocks {self.blocked_id}"


class ModerationAction(models.Model):
    """A moderator action taken against a target (user/activity/post), with a reason."""

    class Action(models.TextChoices):
        WARN = "warn", "Warn"
        REMOVE = "remove", "Remove content"
        SUSPEND = "suspend", "Suspend account"
        BAN = "ban", "Ban account"

    moderator = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        related_name="moderation_actions",
    )
    target_type = models.ForeignKey(ContentType, on_delete=models.CASCADE)
    target_id = models.PositiveBigIntegerField()
    target = GenericForeignKey("target_type", "target_id")

    action = models.CharField(max_length=16, choices=Action.choices)
    reason = models.CharField(max_length=24, choices=ReasonCode.choices)
    notes = models.TextField(blank=True)
    report = models.ForeignKey(
        Report, on_delete=models.SET_NULL, null=True, blank=True, related_name="actions"
    )
    # For SUSPEND: when the suspension elapses (null = indefinite). lifted_at records
    # when an expired suspension was reversed (account reactivated).
    expires_at = models.DateTimeField(null=True, blank=True)
    lifted_at = models.DateTimeField(null=True, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [
            models.Index(fields=["target_type", "target_id"]),
            models.Index(fields=["action", "expires_at"]),
        ]

    def __str__(self):
        return f"{self.action} ({self.reason})"


class AuditLog(models.Model):
    """Append-only, hash-chained log of safety-relevant events (tamper-evident).

    Each row's `hash` covers its content plus the previous row's hash, so any
    edit/deletion of history is detectable via `verify_audit_chain()`.
    """

    actor = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="audit_events",
    )
    event = models.CharField(max_length=64)
    target_ref = models.CharField(max_length=128, blank=True)
    data = models.JSONField(default=dict, blank=True)
    # Not auto_now_add: the hash covers this exact value, so it must be set explicitly.
    created_at = models.DateTimeField(default=timezone.now)
    prev_hash = models.CharField(max_length=64, blank=True)
    hash = models.CharField(max_length=64, db_index=True)

    class Meta:
        ordering = ["id"]

    def __str__(self):
        return f"audit#{self.pk}:{self.event}"
