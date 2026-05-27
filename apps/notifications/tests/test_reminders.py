import pytest
from django.core.management import call_command

from apps.notifications.models import Notification, NotificationType
from apps.notifications.services import get_preferences
from apps.social.models import Membership


@pytest.mark.django_db
def test_reminder_sent_to_members_once(activity, user):
    Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    call_command("send_activity_reminders", "--within-hours", "24")
    reminders = Notification.objects.filter(recipient=user, ntype=NotificationType.EVENT_REMINDER)
    assert reminders.count() == 1

    # Idempotent: a second run does not duplicate.
    call_command("send_activity_reminders", "--within-hours", "24")
    assert reminders.count() == 1


@pytest.mark.django_db
def test_reminder_respects_opt_out(activity, user):
    Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    pref = get_preferences(user)
    pref.event_reminders = False
    pref.save()
    call_command("send_activity_reminders")
    assert Notification.objects.filter(recipient=user).count() == 0
