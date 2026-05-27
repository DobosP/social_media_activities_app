import pytest
from django.utils import timezone

from apps.chat import services
from apps.chat.models import ChatMessage
from apps.social.models import Activity

pytestmark = pytest.mark.django_db


def test_member_can_send_and_read(thread, owner, member):
    services.send_message(owner, thread, "hello")
    services.send_message(member, thread, "hi there")
    history = services.message_history(thread)
    assert [m.body for m in history] == ["hello", "hi there"]


def test_outsider_cannot_access(thread, outsider):
    assert services.can_access_thread(outsider, thread) is False
    with pytest.raises(services.ChatError):
        services.send_message(outsider, thread, "let me in")


def test_cohort_isolation(thread, teen):
    # A teen is a different cohort than the adult activity; no access even if added.
    assert services.can_access_thread(teen, thread) is False


def test_empty_message_rejected(thread, owner):
    with pytest.raises(services.ChatError):
        services.send_message(owner, thread, "   ")


def test_length_cap(thread, owner, settings):
    settings.CHAT_MAX_LENGTH = 5
    with pytest.raises(services.ChatError):
        services.send_message(owner, thread, "way too long")


def test_rate_limit(thread, owner, settings):
    settings.CHAT_RATE_LIMIT = 2
    settings.CHAT_RATE_WINDOW_SECONDS = 60
    services.send_message(owner, thread, "1")
    services.send_message(owner, thread, "2")
    with pytest.raises(services.ChatError):
        services.send_message(owner, thread, "3")


def test_closed_activity_blocks_send(thread, owner):
    thread.activity.status = Activity.Status.CANCELLED
    thread.activity.save(update_fields=["status"])
    with pytest.raises(services.ChatError):
        services.send_message(owner, thread, "anyone there?")


def test_purge_expired(thread, owner, settings):
    settings.CHAT_RETENTION_DAYS = 7
    old = services.send_message(owner, thread, "old")
    ChatMessage.objects.filter(pk=old.pk).update(
        created_at=timezone.now() - timezone.timedelta(days=10)
    )
    services.send_message(owner, thread, "fresh")
    removed = services.purge_expired()
    assert removed == 1
    assert list(ChatMessage.objects.values_list("body", flat=True)) == ["fresh"]
