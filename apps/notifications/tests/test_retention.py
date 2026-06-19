"""P1 storage hygiene: purge_read_notifications deletes old READ, non-DSA notifications but keeps
unread ones, recent ones, and the DSA-mandated MODERATION/SYSTEM notices forever."""

from datetime import timedelta

import pytest
from django.contrib.auth import get_user_model
from django.core.management import call_command
from django.test import override_settings
from django.utils import timezone

from apps.notifications.models import Notification

pytestmark = pytest.mark.django_db
User = get_user_model()


def _notif(user, *, kind, read, age_days):
    n = Notification.objects.create(
        recipient=user,
        kind=kind,
        title="x",
        read_at=timezone.now() if read else None,
    )
    # created_at is auto_now_add; backdate it via an update (bypasses auto_now_add).
    Notification.objects.filter(pk=n.pk).update(
        created_at=timezone.now() - timedelta(days=age_days)
    )
    return n


@override_settings(NOTIFICATION_RETENTION_DAYS=180)
def test_purges_only_old_read_non_dsa():
    u = User.objects.create_user(username="ret", password="pw")
    old_read = _notif(u, kind=Notification.Kind.JOIN_APPROVED, read=True, age_days=400)
    old_unread = _notif(u, kind=Notification.Kind.JOIN_APPROVED, read=False, age_days=400)
    recent_read = _notif(u, kind=Notification.Kind.JOIN_APPROVED, read=True, age_days=10)
    old_read_moderation = _notif(u, kind=Notification.Kind.MODERATION, read=True, age_days=400)
    old_read_system = _notif(u, kind=Notification.Kind.SYSTEM, read=True, age_days=400)

    call_command("purge_read_notifications")

    surviving = set(Notification.objects.values_list("pk", flat=True))
    assert old_read.pk not in surviving  # old + read + non-DSA -> purged
    assert old_unread.pk in surviving  # unread is kept (user hasn't seen it)
    assert recent_read.pk in surviving  # within retention
    assert old_read_moderation.pk in surviving  # DSA Art.17 — never purged
    assert old_read_system.pk in surviving  # DSA Art.16 — never purged


@override_settings(NOTIFICATION_RETENTION_DAYS=0)
def test_retention_disabled_purges_nothing():
    u = User.objects.create_user(username="ret0", password="pw")
    old_read = _notif(u, kind=Notification.Kind.JOIN_APPROVED, read=True, age_days=400)
    call_command("purge_read_notifications")
    assert Notification.objects.filter(pk=old_read.pk).exists()
