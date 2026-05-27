from django.conf import settings
from django.utils import timezone

from apps.safety.services import allow_action
from apps.social.models import Activity, Membership

from .models import ChatMessage
from .policy import get_message_policy


class ChatError(Exception):
    """A chat access or content rule was violated."""


def can_access_thread(user, thread) -> bool:
    """A user may read/write a thread only as an active member of its activity,
    within the same age cohort (cohort isolation, see docs/SAFETY.md)."""
    if not user or not user.is_authenticated:
        return False
    activity = thread.activity
    if user.cohort != activity.cohort:
        return False
    return activity.memberships.filter(user=user, state=Membership.State.MEMBER).exists()


def assert_can_access(user, thread) -> None:
    if not can_access_thread(user, thread):
        raise ChatError("You are not a member of this activity's thread.")


def send_message(author, thread, body: str) -> ChatMessage:
    assert_can_access(author, thread)
    if thread.activity.status != Activity.Status.OPEN:
        raise ChatError("This activity is closed.")

    limit = getattr(settings, "CHAT_RATE_LIMIT", 30)
    window = getattr(settings, "CHAT_RATE_WINDOW_SECONDS", 60)
    if not allow_action(author, "chat_send", limit=limit, window_seconds=window):
        raise ChatError("You are sending messages too quickly; slow down.")

    result = get_message_policy().process(author=author, thread=thread, body=body)
    if not result.allowed:
        raise ChatError(result.reason or "Message rejected by policy.")

    return ChatMessage.objects.create(
        thread=thread, author=author, body=result.body, redacted=result.redacted
    )


def message_history(thread, *, limit: int = 50) -> list[ChatMessage]:
    qs = thread.chat_messages.select_related("author").order_by("-created_at")[:limit]
    return list(reversed(qs))


def purge_expired(now=None) -> int:
    """Delete messages older than the retention window. Returns the count removed."""
    days = getattr(settings, "CHAT_RETENTION_DAYS", 0)
    if not days:
        return 0
    cutoff = (now or timezone.now()) - timezone.timedelta(days=days)
    deleted, _ = ChatMessage.objects.filter(created_at__lt=cutoff).delete()
    return deleted
