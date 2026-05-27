import pytest

from apps.notifications.models import Notification, NotificationType
from apps.notifications.services import (
    get_preferences,
    mark_read,
    notify,
    unread_count,
)


@pytest.mark.django_db
def test_notify_creates_and_counts(user):
    n = notify(user, NotificationType.JOIN_APPROVED, title="Approved", body="welcome")
    assert n is not None
    assert unread_count(user) == 1
    assert Notification.objects.filter(recipient=user).count() == 1


@pytest.mark.django_db
def test_notify_respects_opt_out(user):
    pref = get_preferences(user)
    pref.activity_updates = False
    pref.save()
    assert notify(user, NotificationType.JOIN_APPROVED, title="x") is None
    assert unread_count(user) == 0


@pytest.mark.django_db
def test_unknown_type_raises(user):
    with pytest.raises(ValueError):
        notify(user, "bogus", title="x")


@pytest.mark.django_db
def test_mark_read(user):
    notify(user, NotificationType.SYSTEM, title="a")
    notify(user, NotificationType.SYSTEM, title="b")
    assert unread_count(user) == 2
    marked = mark_read(user)
    assert marked == 2
    assert unread_count(user) == 0


@pytest.mark.django_db
def test_mark_read_subset(user):
    n1 = notify(user, NotificationType.SYSTEM, title="a")
    notify(user, NotificationType.SYSTEM, title="b")
    assert mark_read(user, [n1.id]) == 1
    assert unread_count(user) == 1
