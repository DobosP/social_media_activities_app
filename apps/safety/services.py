"""Safety & moderation domain logic: reporting, blocking, moderation actions, a
tamper-evident audit log, and a lightweight rate limiter. See docs/SAFETY.md."""

import hashlib
import json
from collections import namedtuple
from datetime import timedelta

from django.conf import settings
from django.contrib.contenttypes.models import ContentType
from django.core.cache import cache
from django.db import IntegrityError, transaction
from django.db.models import Q
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from .models import (
    AuditLog,
    AuthorityReferral,
    Block,
    ModerationAction,
    ModerationAppeal,
    ReasonCode,
    Report,
)


class RateLimited(Exception):
    """A rate-limited safety action exceeded its window cap (the caller should refuse gently)."""


class AppealError(Exception):
    """A moderation appeal could not be filed/resolved (not self-scoped, duplicate, rate-limited,
    or already decided). The caller maps it to a gentle 4xx — never a 500."""


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
    """Recompute the chain; returns False if any row was tampered with or removed.

    Streams with ``.iterator()`` so verification stays bounded in memory on a never-purged
    (append-only) audit table — materializing every row would blow memory + the timeout at scale."""
    prev_hash = ""
    for row in AuditLog.objects.order_by("id").iterator(chunk_size=2000):
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

# W4-F3: the symmetric DSA loop for a minor. The offender gets the Art.17 statement of reasons and
# the reporter gets the Art.16 outcome — but the legally-responsible guardian of an under-16 learned
# nothing. This SYSTEM notice tells each ACTIVE guardian that a moderation OUTCOME concerning their
# ward occurred, as a PURE POINTER to /wards/: no reason, no who-did-what, no moderator id (the
# Art.17 detail belongs only to the offender). Plain English, matching the sibling alert above.
_MODERATION_GUARDIAN_TITLE = "A moderation decision concerning a child you look after"

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


def _alert_guardians_of_moderation(*minors) -> int:
    """W4-F3: tell each ACTIVE guardian of an affected CHILD that a moderation OUTCOME concerning
    their ward occurred — the symmetric DSA loop (the minor gets the Art.16/17 detail; the
    legally-responsible adult gets a pure pointer). Modelled exactly on _alert_guardians_unsafe:
    keyed strictly on an ACTIVE GuardianRelationship (never a loose flag), blocked pairs excluded,
    SYSTEM (non-mutable, but not baiting — one notice per outcome), savepoint-isolated so a notify
    failure never turns the moderation action into a 500.

    `minors` is the offender+reporter union (either may be None or a non-CHILD; both are dropped).
    Dedup is ACROSS that whole union: a guardian of BOTH the offender and the reporter gets exactly
    ONE notice for one outcome. The body carries ZERO reason/identity/moderator detail — the Art.17
    statement of reasons belongs only to the offender, never the guardian."""
    from django.urls import reverse

    from apps.accounts.models import Cohort, GuardianRelationship
    from apps.notifications.models import Notification
    from apps.notifications.services import notify

    # Only CHILD wards have guardians who must be looped; dedup the affected set first.
    wards = {m for m in minors if m is not None and m.cohort == Cohort.CHILD}
    if not wards:
        return 0
    body = (
        "A moderation decision was made about something concerning a child you look after. "
        "You can see their upcoming meetups on your guardian page."
    )
    url = reverse("wards")
    blocked_cache: dict[int, set[int]] = {}
    notified: set[int] = set()
    for rel in (
        GuardianRelationship.objects.filter(
            ward__in=wards, status=GuardianRelationship.Status.ACTIVE
        )
        .select_related("guardian", "ward")
        .order_by("ward_id", "guardian_id")
    ):
        guardian = rel.guardian
        if guardian.id in notified:
            continue
        # Exclude a guardian blocked vs THIS specific ward (mirror _alert_guardians_unsafe's
        # per-child blocked check); cache per ward so the union stays a couple of cheap queries.
        if rel.ward_id not in blocked_cache:
            blocked_cache[rel.ward_id] = blocked_user_ids(rel.ward)
        if guardian.id in blocked_cache[rel.ward_id]:
            continue
        try:
            # Savepoint so a notify DB failure rolls back ONLY the notify, never the surrounding
            # atomic moderation action, and never turns take_action/dismiss_report into a 500.
            with transaction.atomic():
                notify(
                    guardian,
                    Notification.Kind.SYSTEM,
                    _MODERATION_GUARDIAN_TITLE,
                    body=body,
                    url=url,
                )
        except Exception:
            continue  # best-effort: don't mark a guardian we failed to actually reach
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


_UNSET = object()


def _open_duplicate_counts(reports):
    """One grouped query: {(target_type_id, target_id): open_report_count} over the given reports'
    targets (uses the (target_type, target_id) index). Narrowed by BOTH type and id so the scan is
    bounded to the queue's targets, not every open report of those content types."""
    from django.db.models import Count

    keys = {(r.target_type_id, r.target_id) for r in reports}
    if not keys:
        return {}
    type_ids = {t for t, _ in keys}
    ids = {i for _, i in keys}
    rows = (
        Report.objects.filter(
            status=Report.Status.OPEN, target_type_id__in=type_ids, target_id__in=ids
        )
        .values("target_type_id", "target_id")
        .annotate(n=Count("id"))
    )
    # Over-fetch within the type×id cross product is harmless — only exact (type,id) keys are read.
    return {(row["target_type_id"], row["target_id"]): row["n"] for row in rows}


def _resolve_targets(reports):
    """Bulk-load report targets grouped by content type → {(type_id, target_id): obj}. One query
    per distinct content type (vs a GenericForeignKey load per report). Missing/deleted targets are
    simply absent from the map."""
    by_type = {}
    for r in reports:
        by_type.setdefault(r.target_type_id, set()).add(r.target_id)
    out = {}
    for type_id, ids in by_type.items():
        model = ContentType.objects.get_for_id(type_id).model_class()
        if model is None:
            continue
        for obj in model.objects.filter(pk__in=ids):
            out[(type_id, obj.pk)] = obj
    return out


def _resolve_affected(targets):
    """Map {(type_id, id): affected_user_or_None} for a batch of loaded targets, in ONE user query.
    A User target is itself; otherwise the owner/author/sender/user is read by FK id (already on the
    target row — no per-row query) and the users are fetched in bulk."""
    from django.contrib.auth import get_user_model

    user_model = get_user_model()
    direct, fk = {}, {}
    for key, obj in targets.items():
        if isinstance(obj, user_model):
            direct[key] = obj
            continue
        for attr in ("owner", "author", "sender", "user"):
            uid = getattr(obj, f"{attr}_id", None)
            if uid is not None:
                fk[key] = uid
                break
    users = {u.id: u for u in user_model.objects.filter(id__in=set(fk.values()))} if fk else {}
    out = dict(direct)
    for key, uid in fk.items():
        out[key] = users.get(uid)
    return out


def triage_summary(report, *, open_duplicate_count=None, target=_UNSET, affected=_UNSET):
    """Advisory triage signals for ONE report (staff-only). Returns a fixed-key dict:
      - severity: the reason's severity rank (CSAM/GROOMING highest).
      - involves_child: derived bool — the affected user's cohort is CHILD (never age band/DOB).
      - open_duplicates: count of OPEN reports against the SAME target.
      - contact_hint / contact_terms: the LOWEST-WEIGHT signal — a reported Post body soliciting
        off-platform contact. Empty for non-Post targets.
    No persistence, no per-user rollup, never user-facing. ``target``/``affected``/
    ``open_duplicate_count`` may be passed pre-resolved by triage_order to avoid per-report
    queries."""
    from apps.accounts.models import Cohort
    from apps.social.models import Post

    if target is _UNSET:
        target = report.target
    if affected is _UNSET:
        affected = _affected_user(target) if target is not None else None
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
    if isinstance(target, Post):
        from .triage_keywords import contact_hint_terms

        contact_terms = contact_hint_terms(target.body)

    return {
        "severity": _TRIAGE_SEVERITY.get(report.reason, 0),
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
    """Order reports most-dangerous-first by their triage signals (then newest first as a stable
    final tiebreak). Returns [(report, summary), ...]. Bounded queries regardless of list size:
    one duplicate-count query, one target load per content type, one bulk user load."""
    reports = list(reports)
    dup = _open_duplicate_counts(reports)
    targets = _resolve_targets(reports)
    affected = _resolve_affected(targets)
    pairs = []
    for r in reports:
        key = (r.target_type_id, r.target_id)
        obj = targets.get(key)
        # Populate the GenericForeignKey cache so a downstream serializer's `report.target` reuses
        # the batch-loaded object instead of re-querying per report (would re-introduce the N+1).
        if obj is not None:
            r.target = obj
        pairs.append(
            (
                r,
                triage_summary(
                    r,
                    target=obj,
                    affected=affected.get(key),
                    # Absent from the OPEN-count map => this (non-OPEN) report has 0 open dups.
                    open_duplicate_count=dup.get(key, 0),
                ),
            )
        )
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


def _notify_suspension_lifted(target):
    """Symmetric bookend to the suspension notice — the other half of the DSA Art.17
    moderation lifecycle. When a temporary suspension elapses the account is silently
    reactivated; tell the user calmly that they can participate again, so the lifecycle
    isn't an asymmetric silence. Best-effort: ``lift_expired_suspensions`` is NOT wrapped
    in a single atomic block and saves per-row, so a notification failure must never abort
    the nightly reactivation batch."""
    try:
        from apps.notifications.models import Notification
        from apps.notifications.services import notify

        recipient = _affected_user(target)
        if recipient is None:
            return
        # Own transaction: a DB-level notify failure rolls back only the notification. The
        # reactivation + audit row already committed (this batch runs in autocommit — the
        # caller is not @transaction.atomic), so they can never be undone by it.
        with transaction.atomic():
            notify(
                recipient,
                Notification.Kind.MODERATION,
                title="Your suspension has ended",
                body=(
                    "Your account is active again and you can take part in activities. "
                    "Thanks for your patience."
                ),
                url="",
            )
    except Exception:
        # Symmetric dignity notice is non-critical relative to the reactivation itself.
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
    if action in (
        ModerationAction.Action.SUSPEND,
        ModerationAction.Action.TIMED_BAN,
        ModerationAction.Action.BAN,
    ):
        # Deactivating blocks auth/login for the offending account.
        if hasattr(target, "is_active"):
            target.is_active = False
            target.save(update_fields=["is_active"])
        # A lifetime BAN also blocks the person's wallet from ever re-registering — the
        # identity-keyed ledger survives account erasure (hard recovery, by design).
        if action == ModerationAction.Action.BAN:
            from django.contrib.auth import get_user_model

            if isinstance(target, get_user_model()):
                from apps.accounts.services import ban_identity

                ban_identity(target)
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
    # W4-F3: tell the ACTIVE guardian(s) of the affected CHILD — offender (resolved from the
    # content/account target) AND the reporter — that a moderation outcome concerning their ward
    # occurred. Pure pointer to /wards/, deduped across the offender+reporter union.
    _alert_guardians_of_moderation(
        _affected_user(target), report.reporter if report is not None else None
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
    # W4-F3: a dismissal has no offender — the CHILD reporter is the only minor here, so alert their
    # ACTIVE guardian(s) with the same pure-pointer SYSTEM notice (no detail, no who/why).
    _alert_guardians_of_moderation(report.reporter)
    return report


# Time-limited restrictions that auto-lift when they elapse (a lifetime BAN never does).
_TIMED_RESTRICTIONS = (ModerationAction.Action.SUSPEND, ModerationAction.Action.TIMED_BAN)
# Actions that deactivate the account (block login) — the set that makes a statement of reasons
# unreachable in-app, hence the pre-auth redress surface.
_ACCOUNT_SANCTIONS = (
    ModerationAction.Action.SUSPEND,
    ModerationAction.Action.TIMED_BAN,
    ModerationAction.Action.BAN,
)


def lift_expired_suspensions() -> int:
    """Reactivate accounts whose temporary suspension or timed ban has elapsed, unless a
    lifetime ban or a still-active timed restriction also applies. Returns the number of
    accounts reactivated."""
    from django.contrib.auth import get_user_model

    user_model = get_user_model()
    now = timezone.now()
    reactivated = 0
    expired = ModerationAction.objects.filter(
        action__in=_TIMED_RESTRICTIONS, expires_at__lte=now, lifted_at__isnull=True
    )
    for moderation in expired:
        target = moderation.target
        if isinstance(target, user_model):
            # Only UN-LIFTED actions still restrict — an appeal-overturned sanction stamps
            # lifted_at (see _reverse_action), so without this filter the nightly batch would
            # keep counting an overturned BAN/suspension and never reactivate the account,
            # silently nullifying a granted DSA Art.17 appeal.
            scope = ModerationAction.objects.filter(
                target_type=moderation.target_type,
                target_id=moderation.target_id,
                lifted_at__isnull=True,
            )
            banned = scope.filter(action=ModerationAction.Action.BAN).exists()
            still_suspended = scope.filter(
                action__in=_TIMED_RESTRICTIONS, expires_at__gt=now
            ).exists()
            if not banned and not still_suspended and not target.is_active:
                target.is_active = True
                target.save(update_fields=["is_active"])
                record_audit("moderation.suspension_lifted", target=target)
                # F17: symmetric end-of-suspension dignity notice (best-effort, transaction-
                # isolated so a notify failure can't abort this non-atomic per-row batch).
                _notify_suspension_lifted(target)
                reactivated += 1
        moderation.lifted_at = now
        moderation.save(update_fields=["lifted_at"])
    return reactivated


# --- DSA Art.17 redress: reachable statement of reasons + internal appeal -----------------
# A SUSPEND/TIMED_BAN/BAN sets is_active=False, so the in-app MODERATION statement of reasons is
# unreachable (the offender cannot log in to read it). These give the offender (a) a pre-auth way
# to READ why, and (b) an internal way to CONTEST it — the "you may contest it" the notice promises.

APPEAL_MAX_LEN = 2000  # a contest statement is free text; capped so it can't be abused as storage
APPEAL_RATE_LIMIT = 5  # appeals per window per user (the one-per-action guard also caps spam)
APPEAL_RATE_WINDOW = 86400  # 24h


def _account_restriction_for(user):
    """The single moderation action currently restricting this user's account (most recent active
    SUSPEND / TIMED_BAN / BAN), or None. 'Active' = a lifetime ban, or a timed/indefinite
    restriction not yet expired and not lifted — mirrors lift_expired_suspensions + F19."""
    from django.contrib.auth import get_user_model

    if not isinstance(user, get_user_model()):
        return None
    user_ct = ContentType.objects.get_for_model(get_user_model())
    now = timezone.now()
    qs = ModerationAction.objects.filter(
        target_type=user_ct,
        target_id=user.id,
        action__in=_ACCOUNT_SANCTIONS,
        lifted_at__isnull=True,
    ).filter(
        Q(action=ModerationAction.Action.BAN) | Q(expires_at__isnull=True) | Q(expires_at__gt=now)
    )
    return qs.order_by("-created_at").first()


def restriction_statement_for(user):
    """Self-scoped DSA Art.17 statement of reasons for the restriction currently in force on
    ``user`` — for the pre-auth redress surface. Returns None when the account is not under an
    active moderation restriction (so a self-deactivated / otherwise-inactive account reveals no
    moderation detail). Allowlisted: action + reason + dates + appeal status only — NEVER the
    moderator's identity or private notes.

    Surfaces only the SINGLE most-recent active sanction (``_account_restriction_for``). On the
    rare multi-sanction account the older becomes contestable once the newer is resolved;
    reactivation stays correct because ``_reverse_action`` re-checks every other active sanction."""
    action = _account_restriction_for(user)
    if action is None:
        return None
    try:
        reason_label = ReasonCode(action.reason).label
    except ValueError:
        reason_label = str(action.reason)
    appeal = ModerationAppeal.objects.filter(action=action).first()
    is_lifetime = action.action == ModerationAction.Action.BAN
    return {
        "action_id": action.id,
        "action_label": ModerationAction.Action(action.action).label,
        "reason_label": reason_label,
        "created_at": action.created_at,
        "is_lifetime": is_lifetime,
        # None expires_at on a non-ban = indefinite suspension (no auto-lift date to show).
        "lifts_at": None if is_lifetime else action.expires_at,
        "appeal_status": appeal.status if appeal else None,
        "appeal_status_label": appeal.get_status_display() if appeal else None,
        "can_appeal": appeal is None,
    }


def file_appeal(user, action, statement: str) -> ModerationAppeal:
    """File a DSA Art.17 appeal against a moderation ``action`` that affected ``user``.

    Strictly self-scoped: a user may only contest an action whose AFFECTED user is themselves
    (``_affected_user``), so it can't be used to probe or proxy another account. One appeal per
    action (idempotent — DB-enforced via the OneToOne, with a friendly pre-check), rate-limited,
    and audited. Raises ``AppealError`` (→ a gentle 4xx) on any guard; never a 500."""
    statement = (statement or "").strip()
    if not statement:
        raise AppealError("Please tell us why you think this decision is wrong.")
    if _affected_user(action.target) != user:
        raise AppealError("You can only contest a decision about your own account or content.")
    if ModerationAppeal.objects.filter(action=action).exists():
        raise AppealError("You've already contested this decision; we're reviewing it.")
    if not allow_action(user, "appeal", limit=APPEAL_RATE_LIMIT, window_seconds=APPEAL_RATE_WINDOW):
        raise AppealError("You've contested several decisions recently; please wait a little.")
    try:
        with transaction.atomic():
            appeal = ModerationAppeal.objects.create(
                action=action, appellant=user, statement=statement[:APPEAL_MAX_LEN]
            )
            record_audit("moderation.appeal_filed", actor=user, target=action)
    except IntegrityError as exc:
        # Lost the race against a concurrent identical filing (OneToOne unique) — treat as dup.
        raise AppealError("You've already contested this decision; we're reviewing it.") from exc
    return appeal


def _reverse_action(action) -> bool:
    """Reverse a granted-appeal action, mirroring lift_expired_suspensions. Returns True iff an
    account was reactivated. An account sanction reactivates the target user IF no OTHER un-lifted
    active sanction applies; a REMOVE un-hides the content IF no other un-lifted REMOVE keeps it
    hidden; a WARN has no material effect to undo (the OVERTURNED status is the record). The
    overturned action is marked ``lifted_at`` so it no longer counts as active anywhere."""
    from django.contrib.auth import get_user_model

    now = timezone.now()
    target = action.target
    if target is None:
        return False
    reactivated = False
    if action.action in _ACCOUNT_SANCTIONS and isinstance(target, get_user_model()):
        scope = ModerationAction.objects.filter(
            target_type=action.target_type, target_id=action.target_id, lifted_at__isnull=True
        ).exclude(pk=action.pk)
        other_ban = scope.filter(action=ModerationAction.Action.BAN).exists()
        other_active_timed = (
            scope.filter(action__in=_TIMED_RESTRICTIONS)
            .filter(Q(expires_at__isnull=True) | Q(expires_at__gt=now))
            .exists()
        )
        if action.lifted_at is None:
            action.lifted_at = now
            action.save(update_fields=["lifted_at"])
        if not other_ban and not other_active_timed and not target.is_active:
            target.is_active = True
            target.save(update_fields=["is_active"])
            reactivated = True
        # Overturning a lifetime BAN also releases the wallet from the identity-ban ledger, so the
        # vindicated person can register/recover again (no-op unless IDENTITY_UNIQUENESS_ENFORCED
        # and they were wallet-bound). Only when no OTHER active BAN still keys the same wallet.
        # Deliberately NOT savepoint-isolated (unlike the best-effort notify): the ledger release is
        # part of the atomic appeal outcome — if it fails, the whole resolution should roll back
        # rather than leave the account reactivated but the wallet still on the ban ledger.
        if action.action == ModerationAction.Action.BAN and not other_ban:
            from apps.accounts.services import release_identity_ban

            release_identity_ban(target)
    elif action.action == ModerationAction.Action.REMOVE and hasattr(target, "is_hidden"):
        other_remove = (
            ModerationAction.objects.filter(
                target_type=action.target_type,
                target_id=action.target_id,
                action=ModerationAction.Action.REMOVE,
                lifted_at__isnull=True,
            )
            .exclude(pk=action.pk)
            .exists()
        )
        if action.lifted_at is None:
            action.lifted_at = now
            action.save(update_fields=["lifted_at"])
        if not other_remove and target.is_hidden:
            target.is_hidden = False
            target.save(update_fields=["is_hidden"])
    return reactivated


def _notify_appeal_outcome(action, *, granted: bool):
    """Close the DSA Art.17 loop: tell the affected user how their appeal was decided. Best-effort,
    savepoint-isolated so a notify failure never rolls back the resolution. MODERATION is
    non-mutable; if the account was reactivated the user can read it in-app, and if the decision
    stands the pre-auth restricted surface shows the updated appeal status."""
    try:
        from apps.notifications.models import Notification
        from apps.notifications.services import notify

        recipient = _affected_user(action.target)
        if recipient is None:
            return
        if granted:
            title = "Your appeal succeeded"
            body = (
                "We reviewed your contest of a moderation decision and reversed it. Any "
                "restriction from that decision has been removed."
            )
        else:
            title = "Your appeal was reviewed"
            body = (
                "We reviewed your contest of a moderation decision and the decision stands. "
                "Thank you for letting us take another look."
            )
        with transaction.atomic():
            notify(recipient, Notification.Kind.MODERATION, title=title, body=body, url="")
    except Exception:
        pass


@transaction.atomic
def resolve_appeal(moderator, appeal, *, grant: bool, notes: str = "") -> ModerationAppeal:
    """Moderator decides an appeal. UPHOLD leaves the decision in place; GRANT (overturn) reverses
    it via ``_reverse_action`` (reactivate account / un-hide content). Idempotent: a non-PENDING
    appeal is refused (AppealError). Audited; the affected user gets a non-mutable MODERATION
    outcome notice and, for a CHILD, the active guardian(s) are pinged via the symmetric loop."""
    # Re-read under a row lock so two concurrent resolutions can't both pass the PENDING guard and
    # double-reverse (reactivate twice / duplicate the audit + notify + guardian fan-out).
    appeal = ModerationAppeal.objects.select_for_update().get(pk=appeal.pk)
    if appeal.status != ModerationAppeal.Status.PENDING:
        raise AppealError("This appeal has already been decided.")
    action = appeal.action
    appeal.status = ModerationAppeal.Status.OVERTURNED if grant else ModerationAppeal.Status.UPHELD
    appeal.decided_by = moderator
    appeal.decided_at = timezone.now()
    appeal.decision_notes = notes
    appeal.save(update_fields=["status", "decided_by", "decided_at", "decision_notes"])
    if grant:
        _reverse_action(action)
    record_audit("moderation.appeal_resolved", actor=moderator, target=action, granted=grant)
    _notify_appeal_outcome(action, granted=grant)
    # An appeal outcome is a moderation outcome about the (possibly CHILD) affected user — reuse
    # the W4-F3 symmetric guardian loop (pure /wards/ pointer, ACTIVE GuardianRelationship only).
    _alert_guardians_of_moderation(_affected_user(action.target))
    return appeal


def appeals_for(user, *, limit: int = 50):
    """A user's own appeals, allowlisted (no moderator identity / decision_notes). Newest first."""
    rows = (
        ModerationAppeal.objects.filter(appellant=user)
        .select_related("action")
        .order_by("-created_at")[:limit]
    )
    return [_appeal_summary(a) for a in rows]


def _appeal_summary(appeal) -> dict:
    action = appeal.action
    try:
        reason_label = ReasonCode(action.reason).label
    except ValueError:
        reason_label = str(action.reason)
    return {
        "action_label": ModerationAction.Action(action.action).label,
        "reason_label": reason_label,
        "status": appeal.status,
        "status_label": appeal.get_status_display(),
        "statement": appeal.statement,
        "created_at": appeal.created_at,
        "decided_at": appeal.decided_at,
    }


@transaction.atomic
def create_authority_referral(
    moderator,
    user,
    reason: str,
    *,
    authority: str,
    report=None,
    reference: str = "",
    notes: str = "",
) -> AuthorityReferral:
    """Refer a user to an external authority for behaviour with real-world legal weight.

    Records the referral in the tamper-evident AuditLog and pins it to that entry's hash
    (audit_anchor_hash), so the chain backing it can be proven later. The subject is
    deliberately NOT notified — tipping off a grooming/CSAM suspect can defeat an
    investigation; any account sanction applied alongside carries its own DSA Art.17 notice."""
    referral = AuthorityReferral.objects.create(
        subject_ref=user.public_id,
        reason=reason,
        authority=authority,
        reference=reference,
        report=report,
        referred_by=moderator,
        notes=notes,
    )
    entry = record_audit(
        "authority.referral",
        actor=moderator,
        target=user,
        reason=reason,
        authority=authority,
    )
    # Pin to the referral's own audit entry — always present and unambiguous, vs a tip snapshot
    # that is empty on a fresh chain.
    referral.audit_anchor_hash = entry.hash
    referral.save(update_fields=["audit_anchor_hash"])
    return referral


def referral_proof_pack(referral: AuthorityReferral) -> dict:
    """Read-only, allowlisted proof bundle for a lawful request: the referral metadata plus a
    live verification of the hash-chained audit log. STRICTLY no PII beyond the subject's
    public_id; no moderator identity, no private notes."""
    try:
        reason_label = ReasonCode(referral.reason).label
    except ValueError:
        reason_label = str(referral.reason)
    return {
        "subject_ref": str(referral.subject_ref),
        "reason_label": reason_label,
        "authority": AuthorityReferral.Authority(referral.authority).label,
        "reference": referral.reference,
        "created_at": referral.created_at,
        "anchor_hash": referral.audit_anchor_hash,
        "chain_valid": verify_audit_chain(),
    }


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
    actions = list(
        ModerationAction.objects.filter(action_q)
        .only(
            "action", "reason", "target_type", "target_id", "expires_at", "lifted_at", "created_at"
        )
        .order_by("-created_at")[:limit]
    )
    # One query for the appeal status of every shown decision (no N+1); a decision with no row is
    # contestable, one with a row shows its status and can't be appealed again (one per action).
    appeal_by_action = {
        ap.action_id: ap
        for ap in ModerationAppeal.objects.filter(action_id__in=[a.id for a in actions])
    }
    decisions = []
    for a in actions:
        try:
            reason_label = ReasonCode(a.reason).label
        except ValueError:
            reason_label = str(a.reason)
        appeal = appeal_by_action.get(a.id)
        if a.target_type_id == user_ct.id:
            scope = "your account"
        elif a.target_type_id == activity_ct.id:
            scope = "one of your activities"
        else:
            scope = "one of your posts"
        # A "sanction" restricts the account (suspend / timed ban / lifetime ban), as opposed
        # to a warn/remove. Only sanctions carry an active/expired badge on the F19 page.
        is_lifetime_ban = a.action == ModerationAction.Action.BAN
        is_sanction = a.action in (
            ModerationAction.Action.SUSPEND,
            ModerationAction.Action.TIMED_BAN,
            ModerationAction.Action.BAN,
        )
        decisions.append(
            {
                # action_id is needed to file a contest (the user's own action; not sensitive).
                "action_id": a.id,
                "action_label": ModerationAction.Action(a.action).label,
                "reason_label": reason_label,
                "scope": scope,
                "created_at": a.created_at,
                "is_sanction": is_sanction,
                # "active" = a lifetime ban (never lifts), or a timed restriction not yet
                # expired and not lifted. Only meaningful when is_sanction.
                "is_active": is_sanction
                and (
                    is_lifetime_ban
                    or ((a.expires_at is None or a.expires_at > now) and a.lifted_at is None)
                ),
                # DSA Art.17 contest: contestable until appealed once; then show its status.
                "can_appeal": appeal is None,
                "appeal_status_label": appeal.get_status_display() if appeal else None,
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
