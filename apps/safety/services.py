"""Safety & moderation domain logic: reporting, blocking, moderation actions, a
tamper-evident audit log, and a lightweight rate limiter. See docs/SAFETY.md."""

import hashlib
import json

from django.contrib.contenttypes.models import ContentType
from django.core.cache import cache
from django.db import transaction
from django.db.models import Q
from django.utils import timezone

from .models import AuditLog, Block, ModerationAction, ReasonCode, Report


def _notify_reporter(reporter, title, body):
    """Tell the reporter about their report (DSA Art. 16: acknowledge receipt and notify
    on resolution). Anonymous reports (reporter is None) get no notification. Best-effort:
    a notification failure never blocks the underlying safety action."""
    if reporter is None:
        return
    try:
        from apps.notifications.models import Notification
        from apps.notifications.services import notify

        # Savepoint so a notification DB failure rolls back ONLY the notification, never the
        # surrounding atomic safety action (file_report / take_action / dismiss_report).
        with transaction.atomic():
            notify(reporter, Notification.Kind.SYSTEM, title, body=body)
    except Exception:
        pass


def _canonical(actor_id, event, target_ref, data, created_iso) -> str:
    payload = json.dumps(
        {
            "actor": actor_id,
            "event": event,
            "target": target_ref,
            "data": data,
            "created": created_iso,
        },
        sort_keys=True,
        separators=(",", ":"),
    )
    return hashlib.sha256(payload.encode()).hexdigest()


@transaction.atomic
def record_audit(event: str, *, actor=None, target=None, **data) -> AuditLog:
    """Append a hash-chained audit entry. Serialized via a row lock on the prior tail."""
    prev = AuditLog.objects.select_for_update().order_by("-id").first()
    prev_hash = prev.hash if prev else ""
    target_ref = ""
    if target is not None:
        ct = ContentType.objects.get_for_model(target)
        target_ref = f"{ct.app_label}.{ct.model}:{target.pk}"
    created = timezone.now()
    # Hash over actor_ref (an immutable copy of actor.id), not the FK, so a later
    # SET_NULL on actor (GDPR erasure) doesn't invalidate the chain.
    actor_ref = actor.id if actor else None
    digest = _canonical(actor_ref, event, target_ref, data, created.isoformat())
    chained = hashlib.sha256((prev_hash + digest).encode()).hexdigest()
    return AuditLog.objects.create(
        actor=actor,
        actor_ref=actor_ref,
        event=event,
        target_ref=target_ref,
        data=data,
        created_at=created,
        prev_hash=prev_hash,
        hash=chained,
    )


def verify_audit_chain() -> bool:
    """Recompute the chain; returns False if any row was tampered with or removed."""
    prev_hash = ""
    for row in AuditLog.objects.order_by("id"):
        digest = _canonical(
            row.actor_ref, row.event, row.target_ref, row.data, row.created_at.isoformat()
        )
        expected = hashlib.sha256((prev_hash + digest).encode()).hexdigest()
        if row.prev_hash != prev_hash or row.hash != expected:
            return False
        prev_hash = row.hash
    return True


@transaction.atomic
def file_report(reporter, target, reason, detail="") -> Report:
    ct = ContentType.objects.get_for_model(target)
    report = Report.objects.create(
        reporter=reporter,
        target_type=ct,
        target_id=target.pk,
        reason=reason,
        detail=detail,
    )
    record_audit("report.filed", actor=reporter, target=target, reason=reason)
    _notify_reporter(
        reporter,
        "We received your report",
        "Thanks - your report was sent to the moderation team. We'll let you know once "
        "it's been reviewed.",
    )
    return report


@transaction.atomic
def block_user(blocker, blocked) -> Block:
    if blocker.id == blocked.id:
        raise ValueError("A user cannot block themselves.")
    block, created = Block.objects.get_or_create(blocker=blocker, blocked=blocked)
    if created:
        record_audit("user.blocked", actor=blocker, target=blocked)
    return block


def unblock_user(blocker, blocked) -> None:
    deleted, _ = Block.objects.filter(blocker=blocker, blocked=blocked).delete()
    if deleted:
        record_audit("user.unblocked", actor=blocker, target=blocked)


def is_blocked(viewer, other) -> bool:
    """True if either user has blocked the other (interaction is suppressed both ways)."""
    return Block.objects.filter(
        Q(blocker=viewer, blocked=other) | Q(blocker=other, blocked=viewer)
    ).exists()


def blocked_user_ids(user) -> set[int]:
    """IDs of everyone `user` has blocked or been blocked by — for filtering feeds so
    blocked pairs never see each other's content."""
    if not getattr(user, "id", None):
        return set()
    pairs = Block.objects.filter(Q(blocker=user) | Q(blocked=user)).values_list(
        "blocker_id", "blocked_id"
    )
    return {blocked if blocker == user.id else blocker for blocker, blocked in pairs}


def _affected_user(target):
    """Resolve the user a moderation action affects, for the DSA Art.17 statement of
    reasons. A User target is the user themselves; an Activity is its owner; a Post is
    its author. Returns None when no individual user can be identified (skip notice)."""
    from django.contrib.auth import get_user_model

    if isinstance(target, get_user_model()):
        return target
    # Resolve the content owner across the reportable content models: Activity.owner,
    # Post.author, Message.sender, Membership/Booking.user. Without this, removing a
    # reported message/membership would notify nobody (missing DSA Art.17 notice).
    for attr in ("owner", "author", "sender", "user"):
        if getattr(target, f"{attr}_id", None) is not None:
            return getattr(target, attr, None)
    return None


def _notify_statement_of_reasons(target, action, reason):
    """DSA Art.17: tell the affected user a moderation decision hit their account/content,
    what it was and why, and that they may contest it. Best-effort — never let a
    notification failure roll back or break the moderation action itself."""
    try:
        from apps.notifications.models import Notification
        from apps.notifications.services import notify

        recipient = _affected_user(target)
        if recipient is None:
            return
        action_label = ModerationAction.Action(action).label
        try:
            reason_label = ReasonCode(reason).label
        except ValueError:
            reason_label = str(reason)
        body = (
            f"Action taken: {action_label}. Reason: {reason_label}. "
            "If you believe this decision is wrong, you may contest it."
        )
        # Savepoint: a DB-level failure creating the notification must roll back ONLY the
        # notification, never the (already-applied) moderation action in the outer atomic
        # block — a poisoned transaction would otherwise fail the outer COMMIT.
        with transaction.atomic():
            notify(
                recipient,
                Notification.Kind.MODERATION,
                title="A moderation decision affected your content/account",
                body=body,
                url="",
            )
    except Exception:
        # Statement-of-reasons delivery is non-critical relative to the action itself.
        pass


@transaction.atomic
def take_action(moderator, target, action, reason, *, notes="", report=None, expires_at=None):
    """Apply a moderation action and record it. Suspend/ban deactivate the target user."""
    record = ModerationAction.objects.create(
        moderator=moderator,
        target_type=ContentType.objects.get_for_model(target),
        target_id=target.pk,
        action=action,
        reason=reason,
        notes=notes,
        report=report,
        expires_at=expires_at,
    )
    if action in (ModerationAction.Action.SUSPEND, ModerationAction.Action.BAN):
        # Deactivating blocks auth/login for the offending account.
        if hasattr(target, "is_active"):
            target.is_active = False
            target.save(update_fields=["is_active"])
    elif action == ModerationAction.Action.REMOVE:
        # Hide the offending content from every member-facing surface (retained for
        # audit/appeal). Applies to content models that carry an is_hidden flag.
        if hasattr(target, "is_hidden"):
            target.is_hidden = True
            target.save(update_fields=["is_hidden"])
    if report is not None:
        report.status = Report.Status.ACTIONED
        report.handled_by = moderator
        report.handled_at = timezone.now()
        report.save(update_fields=["status", "handled_by", "handled_at"])
    record_audit(
        "moderation.action",
        actor=moderator,
        target=target,
        action=action,
        reason=reason,
    )
    _notify_statement_of_reasons(target, action, reason)  # DSA Art.17 (to the offender)
    if report is not None:
        _notify_reporter(  # DSA Art.16 outcome notice (to the reporter)
            report.reporter,
            "Your report was reviewed",
            "Thanks for your report. Our moderation team reviewed it and took action.",
        )
    return record


@transaction.atomic
def dismiss_report(moderator, report: Report, resolution: str = "") -> Report:
    report.status = Report.Status.DISMISSED
    report.handled_by = moderator
    report.handled_at = timezone.now()
    report.resolution = resolution
    report.save(update_fields=["status", "handled_by", "handled_at", "resolution"])
    record_audit("report.dismissed", actor=moderator, target=report)
    _notify_reporter(
        report.reporter,
        "Your report was reviewed",
        "Thanks for your report. Our moderation team reviewed it and found no action was needed.",
    )
    return report


def lift_expired_suspensions() -> int:
    """Reactivate accounts whose temporary suspension has elapsed, unless a ban or a
    still-active suspension also applies. Returns the number of accounts reactivated."""
    from django.contrib.auth import get_user_model

    user_model = get_user_model()
    now = timezone.now()
    reactivated = 0
    expired = ModerationAction.objects.filter(
        action=ModerationAction.Action.SUSPEND, expires_at__lte=now, lifted_at__isnull=True
    )
    for moderation in expired:
        target = moderation.target
        if isinstance(target, user_model):
            scope = ModerationAction.objects.filter(
                target_type=moderation.target_type, target_id=moderation.target_id
            )
            banned = scope.filter(action=ModerationAction.Action.BAN).exists()
            still_suspended = scope.filter(
                action=ModerationAction.Action.SUSPEND, expires_at__gt=now
            ).exists()
            if not banned and not still_suspended and not target.is_active:
                target.is_active = True
                target.save(update_fields=["is_active"])
                record_audit("moderation.suspension_lifted", target=target)
                reactivated += 1
        moderation.lifted_at = now
        moderation.save(update_fields=["lifted_at"])
    return reactivated


def allow_action(user, action: str, *, limit: int, window_seconds: int) -> bool:
    """Simple fixed-window rate limiter (anti-abuse). Returns False when over the limit."""
    key = f"ratelimit:{action}:{user.id}"
    count = cache.get_or_set(key, 0, window_seconds)
    if count >= limit:
        return False
    try:
        cache.incr(key)
    except ValueError:
        cache.set(key, 1, window_seconds)
    return True


def safety_record_for(user, *, limit: int = 50) -> dict:
    """Read-only DSA Art.16/17 record for ONE user (F19): the moderation decisions that
    affected their own account/activities/posts, and the status of the reports they filed.

    STRICTLY self-only: it never exposes another user's data, the moderator's identity, the
    moderator's private notes, or who/what was reported. Each row is projected to an
    allowlisted dict — the ORM rows themselves (with target/moderator/handler FKs) never
    leave this function.

    Scope note: "affecting you" covers User / Activity-owner / Post-author targets. Moderation
    of a user's Message or Booking is deliberately omitted here, and the page header is
    narrowed to match; extend the Q below + the scope labels if those surfaces are added.
    """
    from django.contrib.auth import get_user_model

    from apps.social.models import Activity, Post

    user_ct = ContentType.objects.get_for_model(get_user_model())
    activity_ct = ContentType.objects.get_for_model(Activity)
    post_ct = ContentType.objects.get_for_model(Post)
    # Intentional caps so the "own content" id sets can't blow up the IN clause.
    own_activity_ids = list(Activity.objects.filter(owner=user).values_list("id", flat=True)[:500])
    own_post_ids = list(Post.objects.filter(author=user).values_list("id", flat=True)[:1000])
    now = timezone.now()

    action_q = (
        Q(target_type=user_ct, target_id=user.id)
        | Q(target_type=activity_ct, target_id__in=own_activity_ids)
        | Q(target_type=post_ct, target_id__in=own_post_ids)
    )
    decisions = []
    for a in (
        ModerationAction.objects.filter(action_q)
        .only(
            "action", "reason", "target_type", "target_id", "expires_at", "lifted_at", "created_at"
        )
        .order_by("-created_at")[:limit]
    ):
        try:
            reason_label = ReasonCode(a.reason).label
        except ValueError:
            reason_label = str(a.reason)
        if a.target_type_id == user_ct.id:
            scope = "your account"
        elif a.target_type_id == activity_ct.id:
            scope = "one of your activities"
        else:
            scope = "one of your posts"
        is_suspension = a.action == ModerationAction.Action.SUSPEND
        decisions.append(
            {
                "action_label": ModerationAction.Action(a.action).label,
                "reason_label": reason_label,
                "scope": scope,
                "created_at": a.created_at,
                "is_suspension": is_suspension,
                # Only meaningful for a suspension; "active" = not yet expired and not lifted.
                "is_active": is_suspension
                and (a.expires_at is None or a.expires_at > now)
                and a.lifted_at is None,
            }
        )

    reports = []
    for r in (
        Report.objects.filter(reporter=user)
        .only("reason", "status", "detail", "created_at", "handled_at", "resolution")
        .order_by("-created_at")[:limit]
    ):
        try:
            reason_label = ReasonCode(r.reason).label
        except ValueError:
            reason_label = str(r.reason)
        reports.append(
            {
                "reason_label": reason_label,
                "status_label": Report.Status(r.status).label,
                "created_at": r.created_at,
                "handled_at": r.handled_at,
                "detail": r.detail,  # the reporter's OWN submitted text — safe to show back
                "resolution": r.resolution,
            }
        )

    return {"decisions": decisions, "reports": reports}
