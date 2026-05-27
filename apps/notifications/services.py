"""Notifications domain logic.

`notify()` is the single entry point other apps call (e.g. social on join-approval).
It is **opt-in and privacy-respecting**: it checks the recipient's preferences, writes
an in-app record, and fans out to any configured extra channels. No behavioural
tracking; payloads carry only non-PII context about the recipient's own activity.
"""

from __future__ import annotations

import logging

from django.utils import timezone

from .channels import InAppChannel, extra_channels
from .models import Notification, NotificationPreference, NotificationType

logger = logging.getLogger(__name__)


def get_preferences(user) -> NotificationPreference:
    pref, _ = NotificationPreference.objects.get_or_create(user=user)
    return pref


def notify(recipient, ntype: str, *, title: str, body: str = "", data: dict | None = None):
    """Create + deliver a notification if the recipient opted in to its category.
    Returns the Notification, or None if suppressed by preferences."""
    if ntype not in NotificationType.values:
        raise ValueError(f"unknown notification type: {ntype}")
    if not get_preferences(recipient).allows(ntype):
        return None

    notification = Notification.objects.create(
        recipient=recipient, ntype=ntype, title=title, body=body, data=data or {}
    )
    InAppChannel().deliver(notification)
    for channel in extra_channels():
        try:
            channel.deliver(notification)
        except Exception:  # external channel must never break the request
            logger.warning("notification channel %s failed", channel.name, exc_info=True)
    return notification


def unread_count(user) -> int:
    return Notification.objects.filter(recipient=user, read_at__isnull=True).count()


def mark_read(user, ids: list[int] | None = None) -> int:
    """Mark the user's notifications read (all, or a specific set). Returns count."""
    qs = Notification.objects.filter(recipient=user, read_at__isnull=True)
    if ids is not None:
        qs = qs.filter(id__in=ids)
    return qs.update(read_at=timezone.now())
