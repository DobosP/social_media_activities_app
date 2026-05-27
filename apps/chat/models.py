from django.conf import settings
from django.db import models


class ChatMessage(models.Model):
    """A real-time message inside an activity Thread (D3), private to its members.

    Storage is deliberately minimal: body + author + timestamp. The
    scanning/encryption posture is applied in the message policy (see policy.py),
    kept swappable pending the EU CSAR outcome — see docs/COMPLIANCE.md.
    """

    thread = models.ForeignKey(
        "social.Thread", on_delete=models.CASCADE, related_name="chat_messages"
    )
    author = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="chat_messages"
    )
    body = models.TextField()
    # The moderation pipeline may redact a message's content while keeping the record.
    redacted = models.BooleanField(default=False)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        ordering = ["created_at"]
        indexes = [models.Index(fields=["thread", "created_at"])]

    def __str__(self):
        return f"msg({self.thread_id}, {self.author_id})"
