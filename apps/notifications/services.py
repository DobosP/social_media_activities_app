from django.utils import timezone

from .models import Notification


def notify(recipient, kind, title, *, body="", url="") -> Notification:
    """Create an in-app notification. Safe to call from other apps (leaf dependency)."""
    return Notification.objects.create(
        recipient=recipient, kind=kind, title=title, body=body, url=url
    )


def mark_read(notification: Notification) -> Notification:
    if notification.read_at is None:
        notification.read_at = timezone.now()
        notification.save(update_fields=["read_at"])
    return notification


def mark_all_read(user) -> int:
    return Notification.objects.filter(recipient=user, read_at__isnull=True).update(
        read_at=timezone.now()
    )


def unread_count(user) -> int:
    return Notification.objects.filter(recipient=user, read_at__isnull=True).count()
