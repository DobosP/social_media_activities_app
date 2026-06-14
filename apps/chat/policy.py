from dataclasses import dataclass, field

from django.conf import settings
from django.utils.module_loading import import_string

from apps.chat.presend import scan_text


@dataclass
class ProcessedMessage:
    """Outcome of running a message through the policy pipeline."""

    body: str
    allowed: bool = True
    redacted: bool = False
    reason: str = ""
    # F33: keys of any advisory contact-leak patterns matched (a SOFT signal — see
    # NudgeMessagePolicy). Distinct from ``redacted`` (which means content was scrubbed):
    # nothing is altered or blocked. Callers (post_to_thread / edit_post) ignore this.
    nudge_hits: tuple = field(default_factory=tuple)


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


class NudgeMessagePolicy(BasicMessagePolicy):
    """Default posture + a SOFT, non-blocking pre-send safety signal (F33).

    The real nudge is delivered CLIENT-side (a dismissible "are you sure?" at authorship, so a
    message the author abandons never leaves the device — see ``static/js/presend-nudge.js``).
    This server half is deliberately a pure pass-through: it runs the SAME shared ruleset
    (``apps.chat.presend`` — the single source of truth the client mirrors) over an
    already-accepted body and records any matches on ``nudge_hits``, but it NEVER sets
    ``allowed=False`` on a match, NEVER redacts, and NEVER files a report.

    ``post_to_thread`` / ``edit_post`` read only ``allowed`` and ``body``, so the write path is
    byte-for-byte identical to :class:`BasicMessagePolicy`. This is intentional: auto-reporting
    "meet at my place" would chill legitimate meetup logistics and flood the child-safety queue.
    The human-initiated OFF_PLATFORM report stays the recourse for a genuine concern.
    """

    def process(self, *, author, thread, body: str) -> ProcessedMessage:
        result = super().process(author=author, thread=thread, body=body)
        if result.allowed and result.body:
            hits = scan_text(result.body)
            if hits:
                result.nudge_hits = tuple(hits)
        return result


def get_message_policy() -> MessagePolicy:
    path = getattr(settings, "CHAT_MESSAGE_POLICY", "apps.chat.policy.NudgeMessagePolicy")
    return import_string(path)()
