"""Messaging domain logic.

Two invariants run through everything here:

1. COHORT ISOLATION (anti-grooming): a user may only ever contact, be invited
   by, or share a conversation with another user in the SAME age cohort. An
   adult can therefore never reach a child. The cohort is snapshotted onto the
   Conversation and re-checked on every membership change.
2. ZERO-KNOWLEDGE: the server stores only ciphertext + per-recipient wrapped
   keys. `post_message` never sees plaintext and validates the wrapped-key set
   so a client cannot silently drop recipients or smuggle keys to non-members.

First contact also requires the recipient to ACCEPT (no unsolicited messaging),
and blocking is honoured in both directions. See docs/MESSAGING.md.
"""

from django.conf import settings
from django.contrib.auth import get_user_model
from django.db import transaction
from django.utils import timezone

from apps.accounts.models import Cohort
from apps.safety.services import allow_action, file_report, is_blocked, record_audit

from .models import Conversation, Message, MessageKey, Participant, PublicKey

User = get_user_model()


class MessagingError(Exception):
    """A messaging access, safety, or content rule was violated."""


# --------------------------------------------------------------------------- #
# Key registry
# --------------------------------------------------------------------------- #
def _looks_like_public_jwk(jwk) -> bool:
    """Accept only a public JWK: an EC/OKP key with no private component (`d`)."""
    return (
        isinstance(jwk, dict)
        and jwk.get("kty") in {"EC", "OKP"}
        and "d" not in jwk
        and bool(jwk.get("x"))
    )


@transaction.atomic
def register_public_key(
    user, public_jwk, *, algorithm="ECDH-P256", wrapped_private_jwk=None
) -> PublicKey:
    """Publish (or rotate) a user's identity key. Rejects anything carrying private
    material so the registry can never accidentally hold a private key."""
    if not _looks_like_public_jwk(public_jwk):
        raise MessagingError("A valid public JWK (without private material) is required.")
    if wrapped_private_jwk is not None and not isinstance(wrapped_private_jwk, dict):
        raise MessagingError("wrapped_private_jwk must be an opaque object or omitted.")
    PublicKey.objects.filter(user=user, active=True).update(active=False)
    key = PublicKey.objects.create(
        user=user,
        public_jwk=public_jwk,
        algorithm=algorithm,
        wrapped_private_jwk=wrapped_private_jwk,
    )
    record_audit("messaging.key_registered", actor=user, key_id=str(key.key_id))
    return key


def public_key_for(user) -> PublicKey | None:
    return PublicKey.objects.filter(user=user, active=True).first()


# --------------------------------------------------------------------------- #
# Contact rules — the anti-grooming gate
# --------------------------------------------------------------------------- #
def can_message(initiator, target) -> bool:
    """True if `initiator` is permitted to contact `target`: same (assigned) cohort,
    not blocked either way, both active, and not self. This is the single chokepoint
    that keeps adults and minors from ever reaching each other."""
    if not initiator or not target or initiator.id == target.id:
        return False
    if not getattr(initiator, "is_authenticated", False) or not target.is_active:
        return False
    if initiator.cohort == Cohort.UNASSIGNED or target.cohort == Cohort.UNASSIGNED:
        return False
    if initiator.cohort != target.cohort:
        return False
    if is_blocked(initiator, target):
        return False
    return True


def assert_can_message(initiator, target) -> None:
    if not can_message(initiator, target):
        raise MessagingError("You cannot start a conversation with this user.")


def _rate_ok(user, action, *, limit_setting, default_limit) -> bool:
    limit = getattr(settings, limit_setting, default_limit)
    window = getattr(settings, "MESSAGING_RATE_WINDOW_SECONDS", 60)
    return allow_action(user, action, limit=limit, window_seconds=window)


# --------------------------------------------------------------------------- #
# Starting conversations
# --------------------------------------------------------------------------- #
@transaction.atomic
def start_direct(initiator, target) -> Conversation:
    """Open (or reuse) a 1:1 conversation. The target starts as INVITED and must
    accept before any message can be read by them."""
    assert_can_message(initiator, target)
    existing = (
        Conversation.objects.filter(kind=Conversation.Kind.DIRECT, participants__user=initiator)
        .filter(participants__user=target)
        .distinct()
        .first()
    )
    if existing:
        return existing
    if not _rate_ok(
        initiator, "messaging_start", limit_setting="MESSAGING_START_RATE_LIMIT", default_limit=20
    ):
        raise MessagingError("You are starting conversations too quickly; slow down.")
    conv = Conversation.objects.create(
        kind=Conversation.Kind.DIRECT, cohort=initiator.cohort, creator=initiator
    )
    Participant.objects.create(
        conversation=conv,
        user=initiator,
        state=Participant.State.ACTIVE,
        role=Participant.Role.ADMIN,
        joined_at=timezone.now(),
    )
    Participant.objects.create(
        conversation=conv, user=target, state=Participant.State.INVITED, invited_by=initiator
    )
    record_audit(
        "messaging.direct_started", actor=initiator, target=target, conversation_id=conv.id
    )
    return conv


@transaction.atomic
def start_group(initiator, targets, *, title="") -> Conversation:
    """Open a group conversation. Every target must be contactable (same cohort,
    not blocked) and starts INVITED. The creator is the group admin."""
    unique_targets, seen = [], {initiator.id}
    for t in targets:
        if t.id in seen:
            continue
        seen.add(t.id)
        unique_targets.append(t)
    if not unique_targets:
        raise MessagingError("A group needs at least one other member.")
    for t in unique_targets:
        assert_can_message(initiator, t)
    if not _rate_ok(
        initiator, "messaging_start", limit_setting="MESSAGING_START_RATE_LIMIT", default_limit=20
    ):
        raise MessagingError("You are starting conversations too quickly; slow down.")
    conv = Conversation.objects.create(
        kind=Conversation.Kind.GROUP,
        cohort=initiator.cohort,
        creator=initiator,
        title=(title or "").strip()[:120],
    )
    Participant.objects.create(
        conversation=conv,
        user=initiator,
        state=Participant.State.ACTIVE,
        role=Participant.Role.ADMIN,
        joined_at=timezone.now(),
    )
    for t in unique_targets:
        Participant.objects.create(
            conversation=conv, user=t, state=Participant.State.INVITED, invited_by=initiator
        )
    record_audit(
        "messaging.group_started",
        actor=initiator,
        conversation_id=conv.id,
        invited=len(unique_targets),
    )
    return conv


# --------------------------------------------------------------------------- #
# Membership lifecycle
# --------------------------------------------------------------------------- #
def _participant(conversation, user) -> Participant | None:
    return Participant.objects.filter(conversation=conversation, user=user).first()


@transaction.atomic
def accept_invite(user, conversation) -> Participant:
    p = _participant(conversation, user)
    if not p or p.state != Participant.State.INVITED:
        raise MessagingError("No pending invitation to accept.")
    p.state = Participant.State.ACTIVE
    p.joined_at = timezone.now()
    p.save(update_fields=["state", "joined_at"])
    record_audit("messaging.invite_accepted", actor=user, conversation_id=conversation.id)
    return p


@transaction.atomic
def decline_invite(user, conversation) -> Participant:
    p = _participant(conversation, user)
    if not p or p.state != Participant.State.INVITED:
        raise MessagingError("No pending invitation to decline.")
    p.state = Participant.State.DECLINED
    p.save(update_fields=["state"])
    record_audit("messaging.invite_declined", actor=user, conversation_id=conversation.id)
    return p


@transaction.atomic
def leave(user, conversation) -> Participant:
    p = _participant(conversation, user)
    if not p or p.state not in (Participant.State.ACTIVE, Participant.State.INVITED):
        raise MessagingError("You are not part of this conversation.")
    p.state = Participant.State.LEFT
    p.save(update_fields=["state"])
    record_audit("messaging.left", actor=user, conversation_id=conversation.id)
    return p


@transaction.atomic
def add_participant(actor, conversation, target) -> Participant:
    """Group admins invite new members. The target must share the conversation's
    cohort and be contactable by the actor."""
    if conversation.kind != Conversation.Kind.GROUP:
        raise MessagingError("Only group conversations can add members.")
    actor_p = _participant(conversation, actor)
    if not actor_p or actor_p.state != Participant.State.ACTIVE:
        raise MessagingError("Only active members can add others.")
    if actor_p.role != Participant.Role.ADMIN:
        raise MessagingError("Only a group admin can add members.")
    assert_can_message(actor, target)
    if target.cohort != conversation.cohort:
        raise MessagingError("Members must share the conversation's cohort.")
    existing = _participant(conversation, target)
    if existing and existing.state in (Participant.State.ACTIVE, Participant.State.INVITED):
        return existing
    if existing:
        existing.state = Participant.State.INVITED
        existing.invited_by = actor
        existing.save(update_fields=["state", "invited_by"])
        p = existing
    else:
        p = Participant.objects.create(
            conversation=conversation,
            user=target,
            state=Participant.State.INVITED,
            invited_by=actor,
        )
    record_audit(
        "messaging.participant_added", actor=actor, target=target, conversation_id=conversation.id
    )
    return p


@transaction.atomic
def remove_participant(actor, conversation, target) -> Participant:
    actor_p = _participant(conversation, actor)
    if (
        not actor_p
        or actor_p.role != Participant.Role.ADMIN
        or actor_p.state != Participant.State.ACTIVE
    ):
        raise MessagingError("Only a group admin can remove members.")
    if actor.id == target.id:
        raise MessagingError("Use leave to exit a conversation.")
    p = _participant(conversation, target)
    if not p or p.state in (
        Participant.State.REMOVED,
        Participant.State.LEFT,
        Participant.State.DECLINED,
    ):
        raise MessagingError("That user is not in the conversation.")
    p.state = Participant.State.REMOVED
    p.save(update_fields=["state"])
    record_audit(
        "messaging.participant_removed",
        actor=actor,
        target=target,
        conversation_id=conversation.id,
    )
    return p


# --------------------------------------------------------------------------- #
# Access helpers
# --------------------------------------------------------------------------- #
def is_active_participant(user, conversation) -> bool:
    if not getattr(user, "id", None):
        return False
    return conversation.participants.filter(user=user, state=Participant.State.ACTIVE).exists()


def can_view(user, conversation) -> bool:
    """Only ACTIVE members read content; INVITED users see invitation metadata only."""
    return is_active_participant(user, conversation)


def active_recipient_users(conversation) -> list:
    """Everyone who should receive (and can decrypt) new messages — all ACTIVE
    members, including the sender, so they can read their own history on any device."""
    parts = conversation.participants.filter(state=Participant.State.ACTIVE).select_related("user")
    return [p.user for p in parts]


def conversations_for(user, *, include_pending=True):
    states = [Participant.State.ACTIVE]
    if include_pending:
        states.append(Participant.State.INVITED)
    return (
        Conversation.objects.filter(participants__user=user, participants__state__in=states)
        .distinct()
        .order_by("-updated_at")
    )


# --------------------------------------------------------------------------- #
# Messages — the zero-knowledge write/read path
# --------------------------------------------------------------------------- #
@transaction.atomic
def post_message(
    sender, conversation, *, ciphertext, iv, recipient_keys, algorithm="AES-GCM-256"
) -> Message:
    """Store one ciphertext message plus a wrapped content-key per recipient.

    `recipient_keys` is a list of dicts addressed by recipient public_id:
        {"recipient_public_id", "ephemeral_public_jwk", "wrapped_key", "wrap_iv"}
    The set of recipients must EXACTLY equal the conversation's active members
    (including the sender). This prevents a client from dropping recipients so they
    cannot read, or wrapping keys for someone outside the conversation.
    """
    if not is_active_participant(sender, conversation):
        raise MessagingError("You are not an active member of this conversation.")
    if not ciphertext or not iv:
        raise MessagingError("Encrypted content is required.")
    if not _rate_ok(
        sender, "messaging_send", limit_setting="MESSAGING_SEND_RATE_LIMIT", default_limit=60
    ):
        raise MessagingError("You are sending messages too quickly; slow down.")

    active = {
        str(p.user.public_id): p.user
        for p in conversation.participants.filter(state=Participant.State.ACTIVE).select_related(
            "user"
        )
    }
    rows, provided = [], set()
    for rk in recipient_keys or []:
        pub = str(rk.get("recipient_public_id", ""))
        user = active.get(pub)
        if user is None:
            raise MessagingError("Message keys must address exactly the active members.")
        if pub in provided:
            raise MessagingError("Duplicate recipient key.")
        if not (rk.get("ephemeral_public_jwk") and rk.get("wrapped_key") and rk.get("wrap_iv")):
            raise MessagingError(
                "Each recipient key needs ephemeral_public_jwk, wrapped_key and wrap_iv."
            )
        provided.add(pub)
        rows.append((user, rk))

    if provided != set(active.keys()):
        raise MessagingError("A key must be provided for every active member (including yourself).")

    message = Message.objects.create(
        conversation=conversation, sender=sender, ciphertext=ciphertext, iv=iv, algorithm=algorithm
    )
    MessageKey.objects.bulk_create(
        [
            MessageKey(
                message=message,
                recipient=user,
                ephemeral_public_jwk=rk["ephemeral_public_jwk"],
                wrapped_key=rk["wrapped_key"],
                wrap_iv=rk["wrap_iv"],
            )
            for (user, rk) in rows
        ]
    )
    # Bump updated_at so the inbox sorts by most-recent activity.
    conversation.save(update_fields=["updated_at"])
    record_audit(
        "messaging.message_sent",
        actor=sender,
        conversation_id=conversation.id,
        recipients=len(rows),
    )
    return message


def messages_for(user, conversation, *, limit=50, after_id=None) -> list[Message]:
    """Return messages this user can decrypt (those with a key wrapped to them),
    each annotated with `my_key` (the recipient's own MessageKey)."""
    if not is_active_participant(user, conversation):
        raise MessagingError("You are not an active member of this conversation.")
    qs = (
        Message.objects.filter(conversation=conversation, keys__recipient=user)
        .select_related("sender")
        .distinct()
    )
    if after_id:
        msgs = list(qs.filter(id__gt=after_id).order_by("created_at")[:limit])
    else:
        msgs = list(qs.order_by("-created_at")[:limit])
        msgs.reverse()
    key_map = {
        mk.message_id: mk for mk in MessageKey.objects.filter(message__in=msgs, recipient=user)
    }
    for m in msgs:
        m.my_key = key_map.get(m.id)
    return msgs


# --------------------------------------------------------------------------- #
# Moderation under E2EE: report-with-decryption
# --------------------------------------------------------------------------- #
@transaction.atomic
def report_message(reporter, message, *, reason, detail="", decrypted_excerpt=""):
    """File a safety report against an encrypted message. Because the server cannot
    read ciphertext, the reporter attaches the plaintext THEY can see; moderators act
    on that evidence (and on the sender) via the standard safety tools."""
    if not is_active_participant(reporter, message.conversation):
        raise MessagingError("You can only report messages in your own conversations.")
    excerpt = (decrypted_excerpt or "").strip()[:2000]
    note = (detail or "").strip()
    composed = (
        f"E2EE message report. conversation={message.conversation_id} "
        f"message={message.id} sender={message.sender_id}.\n"
        f"Reporter-decrypted content:\n{excerpt or '(none provided)'}"
    )
    if note:
        composed = f"{note}\n\n{composed}"
    report = file_report(reporter, message, reason, detail=composed)
    record_audit("messaging.message_reported", actor=reporter, target=message, reason=reason)
    return report
