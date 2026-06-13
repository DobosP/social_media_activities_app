"""Safety & moderation domain logic: reporting, blocking, moderation actions, a
tamper-evident audit log, and a lightweight rate limiter. See docs/SAFETY.md."""

import hashlib
import json
from collections import namedtuple
from datetime import timedelta

from django.conf import settings
from django.contrib.contenttypes.models import ContentType
from django.core.cache import cache
from django.db import transaction
from django.db.models import Q
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from .models import AuditLog, Block, ModerationAction, ReasonCode, Report


class RateLimited(Exception):
    """A rate-limited safety action exceeded its window cap (the caller should refuse gently)."""


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


# F8: fixed, server-composed copy for the "I feel unsafe" guardian alert. NEVER child-authored, and
# carries no PII beyond the ward's own name (which their guardian already knows); the meetup details
# live behind the guardian's /wards/ manifest, not in the notice — so this is not a minor->adult
# text channel. Plain English to match the safety app's other notices (file_report's reporter ack).
_UNSAFE_GUARDIAN_TITLE = "Safety alert: a child you look after asked for help"

# A panic-button report is filed with this fixed sentinel in `detail`, so its idempotency check can
# NEVER collide with a user's free-text slow-path report that happens to also be OFF_PLATFORM (which
# would otherwise suppress the guardian alert while the UI claims it fired). It also tells a
# moderator how the report was filed.
_UNSAFE_SENTINEL = "Filed via the one-tap safe-exit “I feel unsafe” button."
# A report still being worked (OPEN/REVIEWING) keeps the fast path idempotent indefinitely; once
# resolved, only the cooldown window guards against an immediate re-storm.
_UNSAFE_NON_TERMINAL = (Report.Status.OPEN, Report.Status.REVIEWING)

# What file_unsafe_report tells the caller: the report row, how many guardians were ACTUALLY alerted
# this call (0 for an adult/teen reporter, a guardian-less or all-blocked child, or an idempotent
# repeat), and whether this tap was an idempotent repeat — so the view's reassurance copy can never
# promise more than what happened.
UnsafeReportResult = namedtuple("UnsafeReportResult", ["report", "guardians_alerted", "repeat"])


def _alert_guardians_unsafe(child) -> int:
    """Fan the F8 SYSTEM safety alert out to the child's ACTIVE guardians, mirroring the
    mark_arrived idiom: keyed strictly on an ACTIVE GuardianRelationship (never a loose flag),
    blocked pairs excluded, at most one per guardian. SYSTEM is non-mutable, so a guardian can never
    accidentally (or maliciously) mute a safety alert. Best-effort + savepoint-isolated: a notify()
    failure rolls back only that notify and never turns the one-tap path into a 500. Returns the
    number of guardians actually alerted (so the caller's reassurance reflects reality)."""
    from django.urls import reverse

    from apps.accounts.models import GuardianRelationship
    from apps.notifications.models import Notification
    from apps.notifications.services import notify

    blocked = blocked_user_ids(child)
    ward_name = child.display_name or child.username
    body = (
        f"{ward_name} used the 'I feel unsafe' button during a meetup. Please check in with them "
        "now. You can see their upcoming meetups on your guardian page."
    )
    url = reverse("wards")
    notified: set[int] = set()
    for rel in GuardianRelationship.objects.filter(
        ward=child, status=GuardianRelationship.Status.ACTIVE
    ).select_related("guardian"):
        guardian = rel.guardian
        if guardian.id in blocked or guardian.id in notified:
            continue
        try:
            # Savepoint so a notify DB failure rolls back ONLY the notify, never the surrounding
            # atomic report — and never turns the scared-child one-tap path into a 500.
            with transaction.atomic():
                notify(
                    guardian, Notification.Kind.SYSTEM, _UNSAFE_GUARDIAN_TITLE, body=body, url=url
                )
        except Exception:
            continue  # best-effort: don't count a guardian we failed to actually reach
        notified.add(guardian.id)
    return len(notified)


@transaction.atomic
def file_unsafe_report(reporter, activity) -> UnsafeReportResult:
    """F8 one-tap "I feel unsafe" for the safe-exit card. Files a real moderation Report on the
    ACTIVITY (OFF_PLATFORM, fixed sentinel detail, no child free text) and — for a CHILD reporter —
    alerts each ACTIVE guardian with a non-mutable SYSTEM notice. The detailed reason-code form
    stays the slow path; this is the scared-kid fast path that reaches a real adult in one tap.

    Idempotent per (reporter, activity): a panic report still being handled (OPEN/REVIEWING) OR
    filed within UNSAFE_REPORT_COOLDOWN_SECONDS makes a re-tap a no-op (returns it, repeat=True),
    so re-taps and post-resolution mashing never re-file or re-storm guardians, while a recurring
    fear after the cooldown can still raise a fresh alert. The dedup is sentinel-scoped, so a user's
    slow-path OFF_PLATFORM report can never suppress the guardian alert. Rate-limited (raises
    RateLimited) as an anti-abuse ceiling; the idempotency check runs FIRST so a child re-tapping
    the SAME activity never burns the budget. Concurrency-safe: a row lock on the activity
    serialises simultaneous taps so they can't both pass the dedup check."""
    from apps.accounts.models import Cohort
    from apps.social.models import Activity

    # Serialise concurrent taps on this activity (TOCTOU): the second tap blocks until the first
    # commits, then sees its report in the dedup check below.
    Activity.objects.select_for_update().filter(pk=activity.pk).first()

    ct = ContentType.objects.get_for_model(activity)
    cooldown = timedelta(seconds=getattr(settings, "UNSAFE_REPORT_COOLDOWN_SECONDS", 300))
    recent = (
        Report.objects.filter(
            reporter=reporter,
            target_type=ct,
            target_id=activity.pk,
            reason=ReasonCode.OFF_PLATFORM,
            detail=_UNSAFE_SENTINEL,
        )
        .order_by("-created_at")
        .first()
    )
    if recent is not None and (
        recent.status in _UNSAFE_NON_TERMINAL or recent.created_at >= timezone.now() - cooldown
    ):
        return UnsafeReportResult(report=recent, guardians_alerted=0, repeat=True)

    if not allow_action(
        reporter,
        "unsafe_report",
        limit=getattr(settings, "UNSAFE_REPORT_RATE_LIMIT", 12),
        window_seconds=getattr(settings, "UNSAFE_REPORT_RATE_WINDOW_SECONDS", 3600),
    ):
        raise RateLimited("Too many safety reports in a short time.")

    report = file_report(reporter, activity, ReasonCode.OFF_PLATFORM, detail=_UNSAFE_SENTINEL)
    alerted = _alert_guardians_unsafe(reporter) if reporter.cohort == Cohort.CHILD else 0
    return UnsafeReportResult(report=report, guardians_alerted=alerted, repeat=False)


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


# --- F11: moderation triage hints (staff-only, advisory, no automated action) --------
# Deterministic ordering signals so a tiny mod team works the most dangerous OPEN reports first.
# Ranks REPORTS, not people; everything is computed live with NO per-user rollup persisted. CHILD
# involvement is a derived boolean — never the age band/DOB.

# Reason severity for triage ordering. CSAM/GROOMING are the top child-safety threats.
_TRIAGE_SEVERITY = {
    ReasonCode.CSAM: 5,
    ReasonCode.GROOMING: 5,
    ReasonCode.OFF_PLATFORM: 3,
    ReasonCode.HARASSMENT: 2,
    ReasonCode.SPAM: 1,
    ReasonCode.OTHER: 0,
}


def _open_duplicate_counts(reports):
    """One grouped query: {(target_type_id, target_id): open_report_count} over the given reports'
    targets (uses the (target_type, target_id) index). Avoids an N+1 across the queue."""
    from django.db.models import Count

    keys = {(r.target_type_id, r.target_id) for r in reports}
    if not keys:
        return {}
    type_ids = {t for t, _ in keys}
    rows = (
        Report.objects.filter(status=Report.Status.OPEN, target_type_id__in=type_ids)
        .values("target_type_id", "target_id")
        .annotate(n=Count("id"))
    )
    return {(row["target_type_id"], row["target_id"]): row["n"] for row in rows}


def triage_summary(report, *, open_duplicate_count=None):
    """Advisory triage signals for ONE open report (staff-only). Returns a fixed-key dict:
      - severity: the reason's severity rank (CSAM/GROOMING highest).
      - involves_child: derived bool — the affected user's cohort is CHILD (never age band/DOB).
      - open_duplicates: count of OPEN reports against the SAME target (pass in for batch use).
      - contact_hint / contact_terms: the LOWEST-WEIGHT signal — a reported Post body soliciting
        off-platform contact. Empty for non-Post targets.
    No persistence, no per-user rollup, never user-facing."""
    from apps.accounts.models import Cohort
    from apps.social.models import Post

    severity = _TRIAGE_SEVERITY.get(report.reason, 0)

    affected = _affected_user(report.target) if report.target is not None else None
    involves_child = bool(
        affected is not None and getattr(affected, "cohort", None) == Cohort.CHILD
    )

    if open_duplicate_count is None:
        open_duplicate_count = Report.objects.filter(
            status=Report.Status.OPEN,
            target_type_id=report.target_type_id,
            target_id=report.target_id,
        ).count()

    contact_terms = []
    if isinstance(report.target, Post):
        from .triage_keywords import contact_hint_terms

        contact_terms = contact_hint_terms(report.target.body)

    return {
        "severity": severity,
        "involves_child": involves_child,
        "open_duplicates": open_duplicate_count,
        "contact_hint": bool(contact_terms),
        "contact_terms": contact_terms,
    }


def triage_rank(summary) -> tuple:
    """Deterministic sort key (DESC) from a triage_summary dict. Severity dominates, then child
    involvement, then duplicate count; the contact_hint is the LAST, lowest-weight tiebreaker, so
    it can never be the sole sort key. Higher tuple sorts first."""
    return (
        summary["severity"],
        1 if summary["involves_child"] else 0,
        summary["open_duplicates"],
        1 if summary["contact_hint"] else 0,
    )


def triage_order(reports):
    """Order a list of OPEN reports most-dangerous-first by their triage signals (then newest
    first as a stable final tiebreak). Returns [(report, summary), ...]. Batches the duplicate
    count into a single query."""
    reports = list(reports)
    dup = _open_duplicate_counts(reports)
    pairs = [
        (
            r,
            triage_summary(r, open_duplicate_count=dup.get((r.target_type_id, r.target_id), 1)),
        )
        for r in reports
    ]
    pairs.sort(key=lambda pair: (triage_rank(pair[1]), pair[0].created_at), reverse=True)
    return pairs


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


# F34: the FIXED allowlist of audit events that represent a DELIBERATE lifecycle action the user
# themselves took on their OWN participation/content (the actor axis), each mapped to plain-language
# copy. An event NOT in this map is DROPPED at the DB, never rendered raw — so a newly-added audit
# event can never leak a raw code or its data payload into a user's view. Deliberately EXCLUDED:
#  - report.filed / messaging.message_reported — both create a Report row already shown on the F19
#    safety record (/my-safety-record/), so listing them here too would double-count;
#  - system-initiated events (group/messaging.participation_revoked, media.*_blocked / *_purged) —
#    they're not something the user chose to do (and most don't match actor_ref anyway);
#  - administration-of-OTHERS and child-safety-sensitive events (messaging.participant_added /
#    removed, guardian observation) — the log is about YOUR participation, not who you manage/watch;
#  - per-message noise (messaging.message_sent).
_ACTIVITY_LOG_LABELS = {
    "activity.arrived": _("You marked yourself as arrived at an activity"),
    "activity.cancelled": _("You cancelled an activity you organised"),
    "connection.requested": _("You sent a connection request"),
    "connection.accepted": _("You accepted a connection"),
    "connection.removed": _("You removed a connection"),
    "group.created": _("You created a group"),
    "group.joined": _("You joined a group"),
    "group.left": _("You left a group"),
    "group.archived": _("You archived a group"),
    "guardian.link_invited": _("You sent a guardian-link invitation"),
    "guardian.link_accepted": _("You accepted a guardian link"),
    "media.uploaded": _("You uploaded a photo"),
    "media.deleted": _("You deleted a photo"),
    "media.attachment_uploaded": _("You uploaded a file to a conversation"),
    "media.attachment_deleted": _("You deleted an attachment"),
    "messaging.direct_started": _("You started a conversation"),
    "messaging.group_started": _("You started a group conversation"),
    "messaging.invite_accepted": _("You joined a conversation"),
    "messaging.invite_declined": _("You declined a conversation invitation"),
    "messaging.key_registered": _("You set up message encryption"),
    "messaging.key_verified": _("You verified a contact's safety number"),
    "messaging.left": _("You left a conversation"),
    "notification.preferences_updated": _("You updated your notification settings"),
    "post.self_deleted": _("You deleted one of your posts"),
    "user.blocked": _("You blocked someone"),
    "user.unblocked": _("You unblocked someone"),
}


def audit_log_for(user, *, limit: int = 100) -> list[dict]:
    """F34: a calm, plain-language list of the safety-relevant actions THIS user took, from the
    tamper-evident audit log. STRICTLY self-scoped to actor_ref == user.id. Each row is projected
    through the FIXED `_ACTIVITY_LOG_LABELS` allowlist (an unmapped event is filtered out at the DB,
    never rendered raw) and emits ONLY {label, when} — never target_ref, the data payload, the raw
    event code, or any other party. Newest first, capped at `limit`."""
    rows = (
        AuditLog.objects.filter(actor_ref=user.id, event__in=list(_ACTIVITY_LOG_LABELS))
        .order_by("-id")
        .values_list("event", "created_at")[:limit]
    )
    return [{"label": str(_ACTIVITY_LOG_LABELS[event]), "when": when} for event, when in rows]
