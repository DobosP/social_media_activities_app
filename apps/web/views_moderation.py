"""ADR-0029 moderation interface — the moderator-gated, server-rendered queue for the soft
``ConcernReview`` items (concern ladder + sensors). These are FORMATIVE, not DSA Art-16 notices:
nothing here restricts content automatically; a human either lets an item rest, relays a gentle
note (teens, human-authored), or ESCALATES into the existing Report tooling (which carries the
statement of reasons + contest rights). Every action is audited inside its transaction.

In ``automated`` MODERATION_MODE the queue still accumulates and the dashboard surfaces the
unattended backlog prominently; nothing is auto-delivered or auto-restricted (fail-safe)."""

from functools import wraps

from django.conf import settings
from django.contrib import messages
from django.contrib.auth.decorators import login_required
from django.db import transaction
from django.db.models import Count
from django.http import HttpResponseForbidden
from django.shortcuts import get_object_or_404, redirect, render
from django.utils import timezone

from apps.notifications.models import Notification
from apps.notifications.services import notify
from apps.safety.models import ConcernReview, ReasonCode
from apps.safety.services import file_report, record_audit
from apps.social.sentiment import FORMATIVE_NOTE_TITLE, _thread_url

# Verbatim teen restorative relay template (ADR-0029 — do NOT paraphrase). Prefilled into the
# moderator's textarea and editable before sending; a human-authored note is the ONLY corrective
# copy that ever reaches a minor (no automated delivery, ever).
TEEN_RELAY_TEMPLATE = (
    "Hi — a few people felt this post might be a bit off-topic for this group. That happens to "
    "everyone; nothing's wrong. Just a gentle heads-up in case you'd like to tweak it. You're "
    "doing great being here."
)

# Only these kinds may be escalated into the Report tooling (ADR-0029). SENSOR_* items are
# informational, moderator-facing detections — not allegations against anyone; SENSOR_PILEON in
# particular PROTECTS the subject, so escalating it would file a Report against the victim. The
# template hides the escalate form for sensor kinds; the handler is the server-side backstop.
_ESCALATABLE_KINDS = frozenset(
    {ConcernReview.Kind.CONCERN_ESCALATED, ConcernReview.Kind.TEEN_CONCERN}
)


def _moderator_required(view):
    """Login + ``is_moderator`` gate (403 otherwise) — the web mirror of the IsModerator DRF
    permission. Placed on every moderation-interface view so a plain member never reaches the
    concern queue."""

    @login_required
    @wraps(view)
    def wrapped(request, *args, **kwargs):
        if not request.user.is_moderator:
            return HttpResponseForbidden("Moderators only.")
        return view(request, *args, **kwargs)

    return wrapped


def _post_snippet(post, *, length=160) -> str:
    """A short, safe preview of a review's post body for the queue list (moderators bypass thread
    membership for moderation reads). Empty when the post was erased/removed (SET_NULL)."""
    if post is None:
        return ""
    body = post.body or ""
    return body if len(body) <= length else body[: length - 1] + "…"


@_moderator_required
def moderation_dashboard(request):
    """`/moderation/` — the unified concern queue. Shows the MODERATION_MODE banner, counts by
    status/kind, and the OPEN items oldest-first (fair queue, mirrors the ModerationAppeal
    status/created_at index). Links to — never duplicates — the admin Report queue."""
    automated = settings.MODERATION_MODE == "automated"
    status_counts = {
        row["status"]: row["n"]
        for row in ConcernReview.objects.values("status").annotate(n=Count("id"))
    }
    open_kind_counts = {
        row["kind"]: row["n"]
        for row in ConcernReview.objects.filter(status=ConcernReview.Status.OPEN)
        .values("kind")
        .annotate(n=Count("id"))
    }
    open_items = list(
        ConcernReview.objects.filter(status=ConcernReview.Status.OPEN)
        .select_related("post", "subject_user")
        .order_by("created_at")[:200]
    )
    rows = [
        {
            "review": review,
            "kind_label": review.get_kind_display(),
            "subject": _subject_label(review.subject_user),
            "snippet": _post_snippet(review.post),
        }
        for review in open_items
    ]
    return render(
        request,
        "web/moderation/dashboard.html",
        {
            "automated": automated,
            "moderation_mode": settings.MODERATION_MODE,
            "status_counts": status_counts,
            "open_kind_counts": open_kind_counts,
            "open_total": len(open_items),
            "rows": rows,
        },
    )


def _subject_label(user) -> str:
    if user is None:
        return "(no longer available)"
    return user.display_name or user.username


@_moderator_required
def moderation_concern(request, pk):
    """`/moderation/concern/<pk>/` — one review item + its actions. GET renders the item; POST
    dispatches on ``action`` (review / dismiss / escalate / send_note). Each action is atomic,
    audited inside the transaction, and stamps status/handled_by/handled_at."""
    review = get_object_or_404(ConcernReview.objects.select_related("post", "subject_user"), pk=pk)
    if request.method == "POST":
        return _handle_concern_action(request, review)

    # A moderator opening the item reads the post body (they bypass thread membership for
    # moderation). That access to member content is auditable — record it (inside its own atomic;
    # record_audit locks the audit tail) whenever a body is actually rendered.
    if review.post is not None:
        with transaction.atomic():
            record_audit("concern.viewed", actor=request.user, target=review)

    is_teen = review.kind == ConcernReview.Kind.TEEN_CONCERN
    return render(
        request,
        "web/moderation/concern_detail.html",
        {
            "review": review,
            "kind_label": review.get_kind_display(),
            "status_label": review.get_status_display(),
            "subject": _subject_label(review.subject_user),
            "post": review.post,
            "post_body": review.post.body if review.post else "",
            "payload_items": sorted((review.payload or {}).items()),
            "is_open": review.status == ConcernReview.Status.OPEN,
            "is_teen": is_teen,
            "is_escalatable": review.kind in _ESCALATABLE_KINDS,
            "teen_template": TEEN_RELAY_TEMPLATE if is_teen else "",
        },
    )


def _handle_concern_action(request, review):
    # Idempotency guard: only an OPEN item may transition. A double-submit (or a stale tab acting on
    # an already-handled item) is rejected with no state change — so, e.g., a double escalate can
    # never file two Reports.
    if review.status != ConcernReview.Status.OPEN:
        messages.error(request, "Already handled.")
        return redirect("moderation_concern", pk=review.pk)
    action = request.POST.get("action", "")
    if action == "review":
        _resolve(review, request.user, ConcernReview.Status.REVIEWED, "concern.reviewed")
        messages.success(request, "Marked reviewed.")
    elif action == "dismiss":
        _resolve(review, request.user, ConcernReview.Status.DISMISSED, "concern.dismissed")
        messages.success(request, "Dismissed.")
    elif action == "escalate":
        _escalate(request, review)
    elif action == "send_note" and review.kind == ConcernReview.Kind.TEEN_CONCERN:
        _send_teen_note(request, review)
    else:
        messages.error(request, "Unknown action.")
    return redirect("moderation_concern", pk=review.pk)


@transaction.atomic
def _resolve(review, moderator, status, event):
    """Stamp a terminal status + handler on a review and audit it (inside the transaction)."""
    review.status = status
    review.handled_by = moderator
    review.handled_at = timezone.now()
    review.save(update_fields=["status", "handled_by", "handled_at"])
    record_audit(event, actor=moderator, target=review)


@transaction.atomic
def _escalate(request, review):
    """Escalate a soft concern into the existing Report tooling (DSA Art-16 path — statement of
    reasons + contest rights) via the canonical ``file_report`` service.

    Restricted to ``_ESCALATABLE_KINDS``: a SENSOR_* item is informational (a moderator-facing
    detection, not an allegation), so escalating it is rejected. The Report target is ALWAYS the
    POST — never ``subject_user``. For SENSOR_PILEON the subject IS the protected victim, so a
    subject-targeted Report would file against them; the kind guard already makes that unreachable,
    and the post-only target is the belt to that suspenders. A missing post (erased/removed) is
    rejected too, never a silent no-op success."""
    if review.kind not in _ESCALATABLE_KINDS:
        messages.error(
            request,
            "This item is informational — a safety signal for you to review, not an allegation, "
            "so it can't be escalated into a report.",
        )
        return
    if review.post is None:
        messages.error(request, "The post is no longer available, so there's nothing to escalate.")
        return
    file_report(
        request.user,
        review.post,
        ReasonCode.OTHER,
        detail=(
            f"Escalated from concern review #{review.pk} ({review.get_kind_display()}). "
            "Formative concern signal reviewed by a moderator and routed to the Report queue."
        ),
    )
    review.status = ConcernReview.Status.ESCALATED
    review.handled_by = request.user
    review.handled_at = timezone.now()
    review.save(update_fields=["status", "handled_by", "handled_at"])
    record_audit("concern.escalated", actor=request.user, target=review)
    messages.success(request, "Escalated into the Report queue.")


@transaction.atomic
def _send_teen_note(request, review):
    """Human-authored teen restorative relay: the ONLY corrective copy that ever reaches a minor,
    and only through this explicit moderator action (never automated). Sends the (edited) note as a
    muteable FORMATIVE_NOTE to the post author, then resolves the review."""
    author = review.subject_user
    if author is None:
        messages.error(request, "The author is no longer available.")
        return
    text = (request.POST.get("note") or "").strip() or TEEN_RELAY_TEMPLATE
    url = _thread_url(review.post.thread.owner_object) if review.post else ""
    note = notify(
        author, Notification.Kind.FORMATIVE_NOTE, FORMATIVE_NOTE_TITLE, body=text, url=url
    )
    # Mark handled regardless — the moderator DID act; the item leaves the queue either way.
    review.status = ConcernReview.Status.REVIEWED
    review.handled_by = request.user
    review.handled_at = timezone.now()
    review.save(update_fields=["status", "handled_by", "handled_at"])
    if note is None:
        # The teen muted this kind, so nothing was delivered. Be honest to the moderator rather
        # than claiming a note was sent, and audit the muted relay distinctly.
        record_audit("concern.note_muted", actor=request.user, target=review)
        messages.warning(request, "The member has muted these notes — nothing was delivered.")
    else:
        record_audit("concern.note_relayed", actor=request.user, target=review)
        messages.success(request, "Gentle note sent.")
