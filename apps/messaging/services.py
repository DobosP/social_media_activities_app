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

import hashlib
import json

from django.conf import settings
from django.contrib.auth import get_user_model
from django.db import transaction
from django.utils import timezone

from apps.accounts.models import Cohort, GuardianRelationship
from apps.accounts.services import can_participate
from apps.safety.services import allow_action, file_report, is_blocked, record_audit

from .models import Conversation, KeyVerification, Message, MessageKey, Participant, PublicKey

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
    if not can_participate(user):
        raise MessagingError(
            "Complete age verification (and parental consent if under 16) before "
            "setting up secure messaging."
        )
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
# Key verification (out-of-band safety numbers — closes the MITM gap)
# --------------------------------------------------------------------------- #
def key_fingerprint(public_jwk) -> str:
    """A stable, human-checkable fingerprint of a public key: the first 32 hex chars
    of SHA-256 over the key's canonical JSON. The browser computes this identically
    (see static/js/e2ee-messaging.js) so the two always agree. The full 60-digit
    *safety number* compared by two users is derived client-side from both parties'
    fingerprints (algorithm documented in docs/MESSAGING.md)."""
    canon = json.dumps(public_jwk, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canon.encode()).hexdigest()[:32]


@transaction.atomic
def record_key_verification(verifier, subject, fingerprint) -> KeyVerification:
    """Record that `verifier` confirmed `subject`'s current key out of band. Rejects a
    fingerprint that doesn't match the subject's *current* active key, so a stale or
    forged value can't be marked verified."""
    assert_can_message(verifier, subject)
    key = public_key_for(subject)
    if key is None:
        raise MessagingError("That user has no active key to verify.")
    current = key_fingerprint(key.public_jwk)
    if fingerprint != current:
        raise MessagingError("Fingerprint does not match the user's current key.")
    obj, _ = KeyVerification.objects.update_or_create(
        verifier=verifier, subject=subject, defaults={"fingerprint": current}
    )
    record_audit("messaging.key_verified", actor=verifier, target=subject)
    return obj


def verification_status(viewer, subject) -> dict:
    """The subject's current key fingerprint and whether `viewer` has a verification
    on record for it. A key rotation changes the fingerprint, so any prior
    verification stops matching automatically (`verified` flips back to False)."""
    key = public_key_for(subject)
    if key is None:
        return {"fingerprint": None, "verified": False}
    fp = key_fingerprint(key.public_jwk)
    verified = KeyVerification.objects.filter(
        verifier=viewer, subject=subject, fingerprint=fp
    ).exists()
    return {"fingerprint": fp, "verified": verified}


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
    # Both parties must have cleared the participation gate — verified age, plus valid
    # parental consent for under-16. Without this a verified-but-non-consented minor
    # could run a full E2EE channel, bypassing the core consent invariant (SAFETY.md #3).
    if not can_participate(initiator) or not can_participate(target):
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
    # Re-assert cohort/consent at accept time: a user's cohort can change between invite
    # and accept (e.g. a corrected age attestation), and a stale snapshot must never land
    # someone in a conversation outside their current cohort (cross-cohort = adult<->minor).
    if user.cohort == Cohort.UNASSIGNED or user.cohort != conversation.cohort:
        raise MessagingError("This conversation is not in your cohort.")
    if not can_participate(user):
        raise MessagingError("Complete age verification (and parental consent if under 16) first.")
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
    _prune_orphaned_guardians(conversation)
    record_audit("messaging.invite_declined", actor=user, conversation_id=conversation.id)
    return p


@transaction.atomic
def leave(user, conversation) -> Participant:
    p = _participant(conversation, user)
    if not p or p.state not in (Participant.State.ACTIVE, Participant.State.INVITED):
        raise MessagingError("You are not part of this conversation.")
    p.state = Participant.State.LEFT
    p.save(update_fields=["state"])
    _prune_orphaned_guardians(conversation)
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
    if p.role == Participant.Role.GUARDIAN:
        # A child must not be able to evict their guardian's oversight; the guardian
        # ends it themselves via leave (or by revoking guardianship at the account level).
        raise MessagingError("A guardian observer cannot be removed by another member.")
    p.state = Participant.State.REMOVED
    p.save(update_fields=["state"])
    _prune_orphaned_guardians(conversation)
    record_audit(
        "messaging.participant_removed",
        actor=actor,
        target=target,
        conversation_id=conversation.id,
    )
    return p


# --------------------------------------------------------------------------- #
# Guardian oversight (consented, transparent, read-only)
# --------------------------------------------------------------------------- #
def _child_wards_in(guardian, conversation):
    """Active CHILD-cohort wards of `guardian` who are active in this conversation."""
    ward_ids = GuardianRelationship.objects.filter(
        guardian=guardian, status=GuardianRelationship.Status.ACTIVE
    ).values_list("ward_id", flat=True)
    return list(
        conversation.participants.filter(
            user_id__in=ward_ids,
            state=Participant.State.ACTIVE,
            user__cohort=Cohort.CHILD,
        ).select_related("user")
    )


@transaction.atomic
def add_guardian_observer(guardian, conversation) -> Participant:
    """Enroll `guardian` as a transparent, read-only observer of a conversation in
    which one of their CHILD wards is an active member. This is the one sanctioned
    cross-cohort presence and exists only because of the consented guardianship.
    Everyone in the conversation can see the guardian (no secret surveillance)."""
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise MessagingError("Only a verified adult guardian can observe a conversation.")
    if not _child_wards_in(guardian, conversation):
        raise MessagingError("You can only observe a conversation your child is part of.")
    if public_key_for(guardian) is None:
        raise MessagingError("Set up secure messaging (a key) before observing.")
    existing = _participant(conversation, guardian)
    if existing and existing.state == Participant.State.ACTIVE:
        return existing
    if existing:
        existing.state = Participant.State.ACTIVE
        existing.role = Participant.Role.GUARDIAN
        existing.joined_at = timezone.now()
        existing.save(update_fields=["state", "role", "joined_at"])
        p = existing
    else:
        p = Participant.objects.create(
            conversation=conversation,
            user=guardian,
            state=Participant.State.ACTIVE,
            role=Participant.Role.GUARDIAN,
            joined_at=timezone.now(),
        )
    record_audit("messaging.guardian_observing", actor=guardian, conversation_id=conversation.id)
    return p


def guardian_observable_conversations(guardian):
    """Conversations a guardian may observe: those where an active CHILD ward is an
    active member. Used by the guardian's discovery view."""
    ward_ids = GuardianRelationship.objects.filter(
        guardian=guardian, status=GuardianRelationship.Status.ACTIVE
    ).values_list("ward_id", flat=True)
    return (
        Conversation.objects.filter(
            participants__user_id__in=ward_ids,
            participants__state=Participant.State.ACTIVE,
            participants__user__cohort=Cohort.CHILD,
        )
        .distinct()
        .order_by("-updated_at")
    )


def _prune_orphaned_guardians(conversation) -> int:
    """End any guardian observer in `conversation` who no longer has an active CHILD
    ward present (the ward left, declined, was removed, or lost participation). A
    guardian's cross-cohort presence exists only for the consented ward and must not
    outlive it — otherwise an adult keeps reading a children's conversation."""
    removed = 0
    guardians = conversation.participants.filter(
        role=Participant.Role.GUARDIAN, state=Participant.State.ACTIVE
    ).select_related("user")
    for gp in guardians:
        if not _child_wards_in(gp.user, conversation):
            gp.state = Participant.State.REMOVED
            gp.save(update_fields=["state"])
            record_audit(
                "messaging.guardian_observer_ended",
                actor=gp.user,
                conversation_id=conversation.id,
            )
            removed += 1
    return removed


@transaction.atomic
def drop_guardian_observers_for(guardian, ward) -> int:
    """Called from accounts when a guardianship is revoked: end `guardian`'s observer
    presence in any conversation whose only basis was this (now-revoked) ward."""
    removed = 0
    observing = Participant.objects.filter(
        user=guardian, role=Participant.Role.GUARDIAN, state=Participant.State.ACTIVE
    ).select_related("conversation")
    for p in observing:
        if not _child_wards_in(guardian, p.conversation):
            p.state = Participant.State.REMOVED
            p.save(update_fields=["state"])
            record_audit(
                "messaging.guardian_observer_ended",
                actor=guardian,
                conversation_id=p.conversation_id,
            )
            removed += 1
    return removed


@transaction.atomic
def remove_user_from_conversations(user, *, reason="participation_revoked") -> int:
    """End a user's messaging presence everywhere — used when they lose participation
    eligibility (e.g. parental consent revoked: "no consent -> no access"). Demotes their
    active/invited rows and prunes any guardian left without a ward as a result."""
    affected = list(
        Conversation.objects.filter(
            participants__user=user,
            participants__state__in=[Participant.State.ACTIVE, Participant.State.INVITED],
        ).distinct()
    )
    Participant.objects.filter(
        user=user, state__in=[Participant.State.ACTIVE, Participant.State.INVITED]
    ).update(state=Participant.State.REMOVED)
    for conv in affected:
        _prune_orphaned_guardians(conv)
    if affected:
        record_audit(
            "messaging.participation_revoked", actor=user, count=len(affected), reason=reason
        )
    return len(affected)


def participant_keys(viewer, conversation) -> list[dict]:
    """Public keys of the conversation's active members, for the client to encrypt to.

    Membership is the authorization here, so this intentionally bypasses the
    cohort-gated key registry — it's how a child member obtains the (cross-cohort)
    public key of a consented guardian observer in order to encrypt to them."""
    if not is_active_participant(viewer, conversation):
        raise MessagingError("You are not an active member of this conversation.")
    out = []
    parts = conversation.participants.filter(state=Participant.State.ACTIVE).select_related("user")
    for p in parts:
        key = public_key_for(p.user)
        if key is None:
            continue
        out.append(
            {
                "public_id": str(p.user.public_id),
                "username": p.user.username,
                "display_name": p.user.display_name or p.user.username,
                "role": p.role,
                "public_jwk": key.public_jwk,
                "fingerprint": key_fingerprint(key.public_jwk),
            }
        )
    return out


# Allowed disappearing-message timers (seconds): off, or 5 min … 30 days.
DISAPPEARING_CHOICES = {0, 300, 3600, 86400, 604800, 2592000}


@transaction.atomic
def set_disappearing(actor, conversation, seconds) -> Conversation:
    """Set the disappearing-messages timer. Any active member can set it for a direct
    chat; only an admin can for a group. The change is audited."""
    try:
        seconds = int(seconds)
    except (TypeError, ValueError):
        raise MessagingError("Timer must be a number of seconds.") from None
    if seconds not in DISAPPEARING_CHOICES:
        raise MessagingError("Unsupported timer value.")
    actor_p = _participant(conversation, actor)
    if not actor_p or actor_p.state != Participant.State.ACTIVE:
        raise MessagingError("You are not an active member of this conversation.")
    if conversation.kind == Conversation.Kind.GROUP and actor_p.role != Participant.Role.ADMIN:
        raise MessagingError("Only a group admin can change the disappearing timer.")
    conversation.disappearing_seconds = seconds
    conversation.save(update_fields=["disappearing_seconds", "updated_at"])
    record_audit(
        "messaging.disappearing_set",
        actor=actor,
        conversation_id=conversation.id,
        seconds=seconds,
    )
    return conversation


def purge_expired_messages(now=None) -> int:
    """Delete ciphertext past its lifetime: per-conversation disappearing timers, plus
    a global MESSAGING_RETENTION_DAYS backstop. Returns the number of messages removed.
    Intended to run periodically (see the purge_messaging management command)."""
    now = now or timezone.now()
    removed = 0
    timed = Conversation.objects.filter(disappearing_seconds__gt=0)
    for conv in timed.iterator():
        cutoff = now - timezone.timedelta(seconds=conv.disappearing_seconds)
        # .delete() returns the cascade total (incl. MessageKey rows); count Messages.
        _, by_model = Message.objects.filter(conversation=conv, created_at__lt=cutoff).delete()
        removed += by_model.get("messaging.Message", 0)
    days = getattr(settings, "MESSAGING_RETENTION_DAYS", 0)
    if days:
        cutoff = now - timezone.timedelta(days=days)
        _, by_model = Message.objects.filter(created_at__lt=cutoff).delete()
        removed += by_model.get("messaging.Message", 0)
    return removed


# --------------------------------------------------------------------------- #
# Access helpers
# --------------------------------------------------------------------------- #
def is_active_participant(user, conversation) -> bool:
    if not getattr(user, "id", None):
        return False
    return conversation.participants.filter(user=user, state=Participant.State.ACTIVE).exists()


def can_view(user, conversation) -> bool:
    """Only ACTIVE members read content; INVITED users see invitation metadata only.
    Guardians are a sanctioned cross-cohort observer; every other member must still be
    participation-eligible, so a revoked-consent minor immediately loses read access."""
    if not is_active_participant(user, conversation):
        return False
    p = _participant(conversation, user)
    if p and p.role == Participant.Role.GUARDIAN:
        return True
    return can_participate(user)


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
    sender_p = _participant(conversation, sender)
    if not sender_p or sender_p.state != Participant.State.ACTIVE:
        raise MessagingError("You are not an active member of this conversation.")
    if sender_p.role == Participant.Role.GUARDIAN:
        # Guardian observers are read-only: an adult must not send into a children's
        # conversation (that would breach cohort isolation for the other child).
        raise MessagingError("Guardian observers can read but not send messages.")
    # A non-guardian sender must still match the conversation's cohort and remain
    # participation-eligible (covers a cohort change or consent revocation after joining).
    if sender.cohort == Cohort.UNASSIGNED or sender.cohort != conversation.cohort:
        raise MessagingError("You can no longer send in this conversation.")
    if not can_participate(sender):
        raise MessagingError("Your participation is not currently active.")
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
    if not can_view(user, conversation):
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
