from django.db import transaction
from django.utils import timezone

from .models import (
    MUTABLE_KINDS,
    NON_MUTABLE_KINDS,
    WHY_REASONS,
    Notification,
    NotificationPreference,
)


def _kind_value(kind) -> str:
    """Normalise a Kind member or bare string to its plain value (call sites pass both)."""
    return str(getattr(kind, "value", kind))


def is_muted(user, kind) -> bool:
    """Whether `user` has muted this kind. DSA non-mutable kinds are NEVER muted (checked
    first, before any DB lookup) — defence in depth with the notify() gate."""
    kv = _kind_value(kind)
    if kv in NON_MUTABLE_KINDS:  # str compares equal to the matching Kind member
        return False
    prefs = NotificationPreference.objects.filter(user=user).first()
    return bool(prefs) and kv in prefs.muted_kinds


def notify(recipient, kind, title, *, body="", url="") -> Notification | None:
    """Create an in-app notification, unless the recipient has muted this kind. Returns the
    Notification, or None if it was muted. Safe to call from other apps (leaf dependency).

    The non-mutable carve-out (MODERATION = DSA Art.17, SYSTEM = DSA Art.16) is checked
    FIRST and is string-based, so those legally-required notices are always delivered even
    if a stale/crafted muted_kinds row names them."""
    kv = _kind_value(kind)
    if kv not in NON_MUTABLE_KINDS and is_muted(recipient, kv):
        return None
    return Notification.objects.create(
        recipient=recipient, kind=kv, title=title, body=body, url=url
    )


def get_muted_kinds(user) -> set[str]:
    prefs = NotificationPreference.objects.filter(user=user).first()
    return set(prefs.muted_kinds) if prefs else set()


@transaction.atomic
def set_muted_kinds(user, kinds) -> NotificationPreference:
    """Persist the user's muted kinds. Any non-mutable (DSA) kind is silently dropped, so a
    user can never mute a legally-required notice even by crafting the request."""
    valid = sorted({kv for kv in map(_kind_value, kinds) if kv in MUTABLE_KINDS})
    prefs, _ = NotificationPreference.objects.update_or_create(
        user=user, defaults={"muted_kinds": valid}
    )
    from apps.safety.services import record_audit

    record_audit("notification.preferences_updated", actor=user, target=user, muted=valid)
    return prefs


def why_reason(kind) -> str:
    """The short 'why you got this' line for a notification kind (empty if unknown)."""
    return WHY_REASONS.get(_kind_value(kind), "")


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
