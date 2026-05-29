"""Cohort-safe, end-to-end-encrypted direct & group messaging.

The server is a ZERO-KNOWLEDGE RELAY: it stores ciphertext and per-recipient
wrapped content-keys but holds no key able to decrypt any message. Private keys
are generated in the browser (Web Crypto) and never leave it in the clear.

Safety is enforced by ACCESS CONTROL rather than content scanning (which is
impossible under E2EE): conversations are confined to a single age cohort
(never adult<->minor), first contact requires the recipient to accept, blocking
is honoured, and abuse is handled via report-with-decryption (the reporter
attaches the plaintext they can see). See docs/MESSAGING.md.
"""

import uuid

from django.conf import settings
from django.db import models

from apps.accounts.models import Cohort


class PublicKey(models.Model):
    """A user's published E2EE identity public key (the key registry).

    `public_jwk` is public material other clients encrypt to. `wrapped_private_jwk`
    is an OPAQUE, client-encrypted backup of the private key (wrapped under a
    passphrase-derived key in the browser) that lets a user restore history on a
    new device. The server can never read either the private key or the passphrase.
    """

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="messaging_keys"
    )
    key_id = models.UUIDField(default=uuid.uuid4, editable=False, unique=True)
    algorithm = models.CharField(max_length=32, default="ECDH-P256")
    public_jwk = models.JSONField()
    wrapped_private_jwk = models.JSONField(null=True, blank=True)
    active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        indexes = [models.Index(fields=["user", "active"])]
        constraints = [
            models.UniqueConstraint(
                fields=["user"],
                condition=models.Q(active=True),
                name="uq_active_key_per_user",
            )
        ]

    def __str__(self):
        return f"key({self.user_id}, {self.algorithm})"


class Conversation(models.Model):
    """A direct (1:1) or group conversation, confined to a single age cohort."""

    class Kind(models.TextChoices):
        DIRECT = "direct", "Direct"
        GROUP = "group", "Group"

    kind = models.CharField(max_length=8, choices=Kind.choices)
    title = models.CharField(max_length=120, blank=True)
    # Snapshot of the cohort this conversation is locked to (cohort isolation).
    cohort = models.CharField(max_length=16, choices=Cohort.choices)
    # Disappearing-messages timer in seconds (0 = off). Messages older than this are
    # purged, minimizing ciphertext at rest. See services.purge_expired_messages.
    disappearing_seconds = models.PositiveIntegerField(default=0)
    creator = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        related_name="created_conversations",
    )
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [models.Index(fields=["cohort", "kind"])]
        ordering = ["-updated_at"]

    def __str__(self):
        return f"{self.kind}({self.id})"


class Participant(models.Model):
    """A user's membership in a conversation, with an invite-accept lifecycle."""

    class State(models.TextChoices):
        INVITED = "invited", "Invited"
        ACTIVE = "active", "Active"
        DECLINED = "declined", "Declined"
        LEFT = "left", "Left"
        REMOVED = "removed", "Removed"

    class Role(models.TextChoices):
        ADMIN = "admin", "Admin"
        MEMBER = "member", "Member"
        # A consented, read-only observer: a verified guardian of a CHILD member.
        # Visible to everyone (no secret surveillance) and cannot send. See SAFETY.md.
        GUARDIAN = "guardian", "Guardian (observer)"

    conversation = models.ForeignKey(
        Conversation, on_delete=models.CASCADE, related_name="participants"
    )
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="conversation_memberships"
    )
    state = models.CharField(max_length=8, choices=State.choices, default=State.INVITED)
    role = models.CharField(max_length=8, choices=Role.choices, default=Role.MEMBER)
    invited_by = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.SET_NULL, null=True, related_name="+"
    )
    created_at = models.DateTimeField(auto_now_add=True)
    joined_at = models.DateTimeField(null=True, blank=True)
    last_read_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["conversation", "user"], name="uq_conversation_user")
        ]
        indexes = [models.Index(fields=["user", "state"])]

    def __str__(self):
        return f"participant({self.conversation_id}, {self.user_id}, {self.state})"

    @property
    def is_active(self) -> bool:
        return self.state == self.State.ACTIVE


class Message(models.Model):
    """A ciphertext-only message. The server stores the AES-GCM ciphertext and its
    IV but holds no key able to decrypt it; per-recipient content keys are in
    MessageKey, each wrapped to a single recipient's public key."""

    conversation = models.ForeignKey(
        Conversation, on_delete=models.CASCADE, related_name="messages"
    )
    sender = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        related_name="sent_messages",
    )
    algorithm = models.CharField(max_length=32, default="AES-GCM-256")
    ciphertext = models.TextField()  # base64 of the AES-GCM ciphertext (incl. tag)
    iv = models.CharField(max_length=64)  # base64 nonce for the content cipher
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        ordering = ["created_at"]
        indexes = [models.Index(fields=["conversation", "created_at"])]

    def __str__(self):
        return f"message({self.conversation_id}, {self.sender_id})"


class MessageKey(models.Model):
    """The per-recipient wrapped content-encryption key (ECIES-style hybrid).

    The sender derives a shared secret via ephemeral ECDH against the recipient's
    public key and wraps the message's content key with it. Only that recipient's
    private key can unwrap it; the server cannot."""

    message = models.ForeignKey(Message, on_delete=models.CASCADE, related_name="keys")
    recipient = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="message_keys"
    )
    ephemeral_public_jwk = models.JSONField()
    wrapped_key = models.TextField()  # base64 — the content key encrypted to the recipient
    wrap_iv = models.CharField(max_length=64)  # base64 nonce for the key-wrap cipher
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["message", "recipient"], name="uq_message_recipient")
        ]
        indexes = [models.Index(fields=["recipient", "message"])]

    def __str__(self):
        return f"key(msg={self.message_id}, to={self.recipient_id})"


class KeyVerification(models.Model):
    """A record that `verifier` confirmed `subject`'s key fingerprint out of band
    (e.g. comparing a safety number in person).

    The server is UNTRUSTED for this: it only mirrors the user's decision across
    devices and is auto-invalidated when the subject's key changes (the stored
    fingerprint no longer matches). The real protection is the human comparison and
    the client's warning when a fingerprint changes. See docs/MESSAGING.md.
    """

    verifier = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="key_verifications_made"
    )
    subject = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.CASCADE,
        related_name="key_verifications_received",
    )
    fingerprint = models.CharField(max_length=128)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["verifier", "subject"], name="uq_verifier_subject")
        ]
        indexes = [models.Index(fields=["verifier", "subject"])]

    def __str__(self):
        return f"verify({self.verifier_id}->{self.subject_id})"
