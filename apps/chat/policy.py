from dataclasses import dataclass

from django.conf import settings
from django.utils.module_loading import import_string


@dataclass
class ProcessedMessage:
    """Outcome of running a message through the policy pipeline."""

    body: str
    allowed: bool = True
    redacted: bool = False
    reason: str = ""


class MessagePolicy:
    """The swappable moderation / scanning / encryption seam for chat.

    CSAR ("Chat Control") is still in trilogue and its scanning/E2EE obligations
    are unsettled, so the chat deliberately does NOT hard-code a stance. Swap in a
    different policy via settings.CHAT_MESSAGE_POLICY to add client/server-side
    scanning, hash-matching, or an encryption envelope without touching the
    transport or storage. See docs/COMPLIANCE.md.
    """

    def process(self, *, author, thread, body: str) -> ProcessedMessage:
        raise NotImplementedError


class BasicMessagePolicy(MessagePolicy):
    """Default posture: trim, enforce a length cap, and reject empty messages.

    No content scanning or encryption yet — that is added by swapping the policy
    once the CSAR requirements are final.
    """

    def process(self, *, author, thread, body: str) -> ProcessedMessage:
        text = (body or "").strip()
        if not text:
            return ProcessedMessage(body="", allowed=False, reason="Message is empty.")
        max_length = getattr(settings, "CHAT_MAX_LENGTH", 4000)
        if len(text) > max_length:
            return ProcessedMessage(
                body="", allowed=False, reason=f"Message exceeds {max_length} characters."
            )
        return ProcessedMessage(body=text)


def get_message_policy() -> MessagePolicy:
    path = getattr(settings, "CHAT_MESSAGE_POLICY", "apps.chat.policy.BasicMessagePolicy")
    return import_string(path)()
