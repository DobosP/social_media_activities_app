"""Domain logic for the social core: cohort-gated activities, join-by-vote, and the
user-place quorum. Views and admin go through these functions so the safety
invariants (cohort isolation, verified-and-consented participation) live in one place.
"""

import re
from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Count, Q
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from apps.accounts.models import Cohort, GuardianRelationship
from apps.accounts.services import can_participate

from .models import (
    DEFAULT_JOIN_THRESHOLD,
    DEFAULT_PLACE_QUORUM,
    Activity,
    Group,
    GroupMembership,
    JoinVote,
    Membership,
    PlaceConfirmation,
    Post,
    Thread,
    UserPlaceProposal,
)

# F3: self-declared arrival ping is only accepted around the start time, and is cleared a
# few hours after start so it never becomes a standing presence record. Overridable via
# settings; sane defaults here.
ARRIVAL_WINDOW_BEFORE_HOURS = 2
ARRIVAL_WINDOW_AFTER_HOURS = 3

# F35 "catch up" digest — deterministic, bounded, no ML. Caps keep the read cheap.
DIGEST_SCAN_LIMIT = 60  # hard cap on non-announcement posts pulled into Python
DIGEST_RECENT_POSTS = 3  # most-recent posts always surfaced
DIGEST_LOGISTICAL_POSTS = 3  # max keyword-matched logistical posts surfaced
DIGEST_MAX_ANNOUNCEMENTS = 2  # latest N announcements
# Conservative, whole-word vocabulary for "this post is about logistics". Deliberately omits
# bare "time" (so "had a great time" never matches); a real time change still trips on
# change/changed/reschedule/moved/postpone. The vocabulary lives only here.
_LOGISTICAL_RE = re.compile(
    r"\b(meet|meeting|change|changed|move|moved|moving|bring|bringing|cancel|"
    r"cancell?ed|cancelling|reschedul\w*|postpon\w*|location|venue)\b",
    re.IGNORECASE,
)


class SocialError(Exception):
    """Base for expected, user-facing social-domain errors."""


class NotEligible(SocialError):
    """User fails the participation/cohort gate for this action."""


class NotAMember(SocialError):
    """Action requires current membership the user doesn't have."""


class InvalidState(SocialError):
    """Target object is not in a state that permits this action."""


class DuplicatePlace(SocialError):
    """A proposed venue duplicates an existing place (F25). Carries the existing place id/name
    so the UI can link to it; ``soft`` marks a near-but-different venue the user may override."""

    def __init__(self, place_id, place_name, *, soft=False):
        self.place_id = place_id
        self.place_name = place_name
        self.soft = soft
        super().__init__(f"A place already exists nearby: {place_name}")


# F25: a stricter same-surface 'don't re-add an existing venue' radius, deliberately separate
# from the 75 m cross-source ingest dedup. Overridable via settings.
PLACE_PROPOSAL_DEDUP_RADIUS_M = 60
PLACE_PROPOSAL_SOFT_RADIUS_M = 25


def _has_cohort(user) -> bool:
    return user.cohort != Cohort.UNASSIGNED


def can_create_activity(user) -> bool:
    return can_participate(user) and _has_cohort(user)


def visible_activities(user):
    """Activities a user may see — those in their own cohort (isolation), excluding
    any owned by a user they've blocked or been blocked by (D4)."""
    if not _has_cohort(user):
        return Activity.objects.none()
    from apps.safety.services import blocked_user_ids

    qs = Activity.objects.filter(cohort=user.cohort, is_hidden=False)
    blocked = blocked_user_ids(user)
    if blocked:
        qs = qs.exclude(owner_id__in=blocked)
    return qs


def can_see_activity(user, activity) -> bool:
    return _has_cohort(user) and user.cohort == activity.cohort


def with_counts(qs):
    """Annotate an Activity queryset with ``member_n`` (current members) and
    ``participant_n`` (members holding a position — excludes supervisory guardians) so
    list serialization needs no per-row COUNT. The serializer reads these annotations
    when present, eliminating the N+1 on the activities feed / recommendations."""
    member = Q(memberships__state=Membership.State.MEMBER)
    return qs.annotate(
        member_n=Count("memberships", filter=member, distinct=True),
        participant_n=Count(
            "memberships",
            filter=member & ~Q(memberships__role=Membership.Role.GUARDIAN),
            distinct=True,
        ),
    )


def current_members(activity):
    return activity.memberships.filter(state=Membership.State.MEMBER)


def voting_members(activity):
    """Members who vote on join requests — peers only; guardians are supervisory and
    do not vote."""
    return current_members(activity).exclude(role=Membership.Role.GUARDIAN)


def thread_members(owner_obj):
    """Current MEMBER-state memberships of a thread owner — an Activity OR a Group. The single
    dispatcher the hardened thread gates (post_to_thread / can_read_thread / toggle_reaction /
    edit_post) use so they hold IDENTICALLY on both surfaces. Fail-closed: an unknown owner type
    raises (never silently treated as an Activity — that default would be a cross-cohort leak)."""
    if isinstance(owner_obj, Activity):
        return owner_obj.memberships.filter(state=Membership.State.MEMBER)
    if isinstance(owner_obj, Group):
        return owner_obj.memberships.filter(state=GroupMembership.State.MEMBER)
    raise TypeError(f"Unknown thread owner type: {type(owner_obj)!r}")


def is_thread_frozen(owner_obj) -> bool:
    """Whether a thread is frozen to new writes: a CANCELLED Activity or a non-ACTIVE (ARCHIVED)
    Group. The freeze gate, generalised. Fail-closed on an unknown owner type."""
    if isinstance(owner_obj, Activity):
        return owner_obj.status == Activity.Status.CANCELLED
    if isinstance(owner_obj, Group):
        return owner_obj.status != Group.Status.ACTIVE
    raise TypeError(f"Unknown thread owner type: {type(owner_obj)!r}")


def participant_count(activity) -> int:
    """Number of participants holding a position — members/owner, excluding guardians."""
    return voting_members(activity).count()


def open_positions(activity) -> int | None:
    """Remaining open spots, or None when the activity is uncapped."""
    if activity.capacity is None:
        return None
    return max(activity.capacity - participant_count(activity), 0)


def can_join(user, activity) -> bool:
    if not can_participate(user):
        return False
    if user.cohort != activity.cohort:
        return False
    if activity.status != Activity.Status.OPEN:
        return False
    if activity.capacity is not None and participant_count(activity) >= activity.capacity:
        return False  # no open positions left
    existing = (
        activity.memberships.filter(user=user).exclude(state=Membership.State.REMOVED).exists()
    )
    return not existing


@transaction.atomic
def create_activity(
    owner,
    *,
    place,
    activity_type,
    title,
    starts_at,
    ends_at=None,
    description="",
    join_threshold=None,
    capacity=None,
    min_to_go=None,
    guardian_accompanied=False,
    meeting_point="",
    what_to_bring="",
    organizer_note="",
    cost_band=Activity.CostBand.UNSPECIFIED,
    difficulty=Activity.Difficulty.UNSPECIFIED,
    accessibility_notes="",
    beginners_welcome=False,
):
    if not can_create_activity(owner):
        raise NotEligible(
            _("User cannot create activities (needs verification/consent + a cohort).")
        )
    if guardian_accompanied and owner.cohort != Cohort.CHILD:
        raise InvalidState(_("Only children's activities can be guardian-accompanied."))
    if min_to_go is not None and capacity is not None and min_to_go > capacity:
        # An un-confirmable meetup (the minimum can never be reached within the cap) is nonsensical.
        raise InvalidState(_("Minimum to happen can't be more than the capacity."))
    # F25 gate: an activity may only be organised at a PUBLICLY-visible place — never at a
    # still-pending/rejected user-proposed venue. public_places() is the single visibility
    # chokepoint, so this holds identically on the web form and the DRF surface.
    from apps.places.services import public_places

    if place is None or not public_places().filter(pk=place.pk).exists():
        raise InvalidState(_("That place isn't available to organise an activity at yet."))
    activity = Activity.objects.create(
        owner=owner,
        place=place,
        activity_type=activity_type,
        title=title,
        description=description,
        starts_at=starts_at,
        ends_at=ends_at,
        cohort=owner.cohort,
        join_threshold=DEFAULT_JOIN_THRESHOLD if join_threshold is None else join_threshold,
        capacity=capacity,
        min_to_go=min_to_go,
        guardian_accompanied=guardian_accompanied,
        meeting_point=meeting_point,
        what_to_bring=what_to_bring,
        organizer_note=organizer_note,
        cost_band=cost_band,
        difficulty=difficulty,
        accessibility_notes=accessibility_notes,
        beginners_welcome=beginners_welcome,
    )
    Membership.objects.create(
        activity=activity,
        user=owner,
        role=Membership.Role.OWNER,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )
    Thread.objects.create(activity=activity)
    return activity


@transaction.atomic
def request_to_join(user, activity) -> Membership:
    if not can_join(user, activity):
        raise NotEligible(_("User is not eligible to join this activity."))
    membership = Membership.objects.create(
        activity=activity,
        user=user,
        role=Membership.Role.MEMBER,
        state=Membership.State.REQUESTED,
    )
    _notify(
        activity.owner,
        "join_requested",
        "New join request",
        body=f"{user.display_name or user.username} asked to join “{activity.title}”.",
        url=f"/api/social/activities/{activity.id}/",
    )
    return membership


@transaction.atomic
def leave_activity(user, activity) -> Membership | None:
    """A member leaves an activity. The owner cannot leave their own activity (they must
    cancel it instead). Returns the removed membership, or None if not a member."""
    membership = activity.memberships.filter(user=user).first()
    if membership is None or membership.state == Membership.State.REMOVED:
        return None
    if membership.role == Membership.Role.OWNER:
        raise InvalidState(_("The owner cannot leave their own activity."))
    membership.state = Membership.State.REMOVED
    # Reset the per-activity transient signals so a removed row carries nothing: the RSVP
    # go/no-go (F20) and the "we met up" confirmation (F22). Keeps both scoped to live members.
    membership.attendance_intent = Membership.AttendanceIntent.UNKNOWN
    membership.met_confirmed_at = None
    membership.save(update_fields=["state", "attendance_intent", "met_confirmed_at", "updated_at"])
    return membership


# Fields an owner may change on an OPEN, not-yet-started activity. Deliberately excludes
# place / activity_type / cohort / owner / guardian_accompanied: those define the meetup's
# identity and the cohort-isolation boundary, so an edit must never touch them (no
# bait-and-switch, no escaping the safety pin). See docs/SAFETY.md.
ACTIVITY_EDITABLE_FIELDS = (
    "title",
    "description",
    "starts_at",
    "ends_at",
    "capacity",
    "min_to_go",  # F1 Quorum-go — owner-curated minimum-to-happen threshold
    "meeting_point",  # F9 logistics — owner-curated, routed through the same edit path
    "what_to_bring",
    "organizer_note",
    "cost_band",  # F8 what-to-expect
    "difficulty",
    "accessibility_notes",
    "beginners_welcome",  # F17 per-activity flag
)


@transaction.atomic
def cancel_activity(owner, activity, *, reason: str = "") -> Activity:
    """Owner cancels a meetup they can no longer host. Flips the activity to CANCELLED
    (so it leaves discovery/joining) and tells every current member, with the reason, so
    nobody travels to a meetup that isn't happening. Idempotent-safe: only an OPEN
    activity can be cancelled."""
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the activity owner may cancel it."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("Only an open activity can be cancelled."))
    activity.status = Activity.Status.CANCELLED
    activity.save(update_fields=["status", "updated_at"])
    reason = (reason or "").strip()[:200]
    body = _("“%(title)s” was cancelled by the organiser.") % {"title": activity.title}
    if reason:
        body = f"{body} {reason}"
    for membership in current_members(activity).exclude(user_id=owner.id).select_related("user"):
        _notify(
            membership.user,
            "activity_cancelled",
            _("An activity was cancelled"),
            body=body,
            url=f"/api/social/activities/{activity.id}/",
        )
    from apps.safety.services import record_audit

    record_audit("activity.cancelled", actor=owner, target=activity, reason=reason)
    return activity


@transaction.atomic
def complete_activity(activity) -> Activity:
    """Move a past OPEN activity to its terminal COMPLETED state. Housekeeping only — no
    notification — so a finished meetup stops being shown as live. No-op unless OPEN."""
    if activity.status != Activity.Status.OPEN:
        return activity
    activity.status = Activity.Status.COMPLETED
    activity.save(update_fields=["status", "updated_at"])
    return activity


def _supersede_reminders(activity) -> None:
    """Clear any already-sent event reminders for this activity so a changed start time
    re-fires one. send_activity_reminders dedups on (recipient, kind, url) and the url
    carries no time, so without this a corrected time would silently never be reminded."""
    from apps.notifications.models import Notification

    Notification.objects.filter(
        kind=Notification.Kind.EVENT_REMINDER,
        url=f"/api/social/activities/{activity.id}/",
    ).delete()


@transaction.atomic
def update_activity(owner, activity, **changes) -> Activity:
    """Owner edits an OPEN, not-yet-started activity in place (preserving its roster,
    thread and vote history). Only ACTIVITY_EDITABLE_FIELDS are honoured; a material time
    change re-notifies members and supersedes the stale reminder."""
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the activity owner may edit it."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("Only an open activity can be edited."))
    if activity.starts_at <= timezone.now():
        raise InvalidState(_("This activity has already started and can no longer be edited."))

    fields = {k: v for k, v in changes.items() if k in ACTIVITY_EDITABLE_FIELDS}
    new_starts = fields.get("starts_at", activity.starts_at)
    new_ends = fields.get("ends_at", activity.ends_at)
    if new_ends is not None and new_ends < new_starts:
        raise InvalidState(_("End time cannot be before the start time."))
    new_capacity = fields.get("capacity", activity.capacity)
    if new_capacity is not None and new_capacity < participant_count(activity):
        raise InvalidState(_("Capacity cannot be lower than the current number of participants."))
    new_min_to_go = fields.get("min_to_go", activity.min_to_go)
    if new_min_to_go is not None and new_capacity is not None and new_min_to_go > new_capacity:
        raise InvalidState(_("Minimum to happen can't be more than the capacity."))

    time_changed = "starts_at" in fields and fields["starts_at"] != activity.starts_at
    if not fields:
        return activity
    for key, value in fields.items():
        setattr(activity, key, value)
    activity.save(update_fields=[*fields.keys(), "updated_at"])

    if "min_to_go" in fields:
        # F1: lowering the threshold can make a still-open meetup cross its (now-lower) minimum on
        # the LIVE count without any new RSVP — fire the one-shot confirm exactly as an RSVP would.
        # _maybe_confirm_meetup's go_confirmed_at guard + OPEN check keep it at-most-once and safe.
        _maybe_confirm_meetup(activity)

    if time_changed:
        _supersede_reminders(activity)
        body = _("“%(title)s” now starts %(when)s.") % {
            "title": activity.title,
            "when": f"{activity.starts_at:%Y-%m-%d %H:%M}",
        }
        for membership in (
            current_members(activity).exclude(user_id=owner.id).select_related("user")
        ):
            _notify(
                membership.user,
                "activity_updated",
                _("An activity you joined changed"),
                body=body,
                url=f"/api/social/activities/{activity.id}/",
            )
    return activity


def _notify(recipient, kind, title, *, body="", url=""):
    """Emit an in-app notification (best-effort; never blocks the social action)."""
    from apps.notifications.services import notify

    notify(recipient, kind, title, body=body, url=url)


def _is_genuinely_new(membership: Membership) -> bool:
    """True when the joiner holds no OTHER current MEMBER membership — i.e. this is their first
    activity. A presence/absence fact about the joiner themselves (never a rating); used to fire
    the first-timer welcome at most once. Self excluded by pk so it's robust to flush order."""
    return not (
        Membership.objects.filter(user_id=membership.user_id, state=Membership.State.MEMBER)
        .exclude(pk=membership.pk)
        .exists()
    )


def _admit(membership: Membership) -> None:
    membership.state = Membership.State.MEMBER
    membership.decided_at = timezone.now()
    body = str(_("You were admitted to “%(title)s”.") % {"title": membership.activity.title})
    # F39: a genuinely-new joiner (their first activity) gets a one-time welcome line on this
    # notification + a self-dismissing banner; welcomed_at makes it at-most-once.
    is_new = membership.welcomed_at is None and _is_genuinely_new(membership)
    update_fields = ["state", "decided_at", "updated_at"]
    if is_new:
        membership.welcomed_at = timezone.now()
        update_fields.append("welcomed_at")
        body += str(
            _(
                " New here? Say a quick hello in the thread and check the meetup logistics — "
                "the group is glad you joined."
            )
        )
    membership.save(update_fields=update_fields)
    _notify(
        membership.user,
        "join_approved",
        "You're in!",
        body=body,
        url=f"/api/social/activities/{membership.activity_id}/",
    )


def _evaluate_vote(membership: Membership) -> None:
    """Promote a requested membership to member once approvals clear the threshold."""
    member_count = voting_members(membership.activity).count()
    if member_count == 0:
        return
    approvals = membership.votes.filter(approve=True).count()
    if approvals / member_count >= membership.activity.join_threshold:
        _admit(membership)


@transaction.atomic
def cast_vote(voter, membership: Membership, approve: bool) -> Membership:
    activity = membership.activity
    if membership.state != Membership.State.REQUESTED:
        raise InvalidState(_("This membership is not awaiting a join vote."))
    if membership.user_id == voter.id:
        raise InvalidState("A requester cannot vote on their own join request.")
    if not voting_members(activity).filter(user=voter).exists():
        raise NotAMember(_("Only current members may vote on join requests."))
    JoinVote.objects.update_or_create(
        membership=membership, voter=voter, defaults={"approve": approve}
    )
    _evaluate_vote(membership)
    return membership


@transaction.atomic
def owner_admit(owner, membership: Membership) -> Membership:
    """Owner override: admit a requested member directly (if enabled for the activity)."""
    activity = membership.activity
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the activity owner may override."))
    if not activity.owner_can_override:
        raise InvalidState(_("Owner override is disabled for this activity."))
    if membership.state != Membership.State.REQUESTED:
        raise InvalidState(_("This membership is not awaiting a join vote."))
    _admit(membership)
    return membership


@transaction.atomic
def add_guardian(owner, activity, guardian) -> Membership:
    """The child owner adds a verified adult as an accompanying guardian (supervisory,
    group-only). Controlled exception to cohort isolation: only on a CHILD-cohort
    activity explicitly flagged guardian_accompanied, and the guardian must be a
    verified adult. Guardians don't vote and aren't open-discoverable. See docs/SAFETY.md.
    """
    from apps.accounts.services import is_guardian_of

    if activity.owner_id != owner.id:
        raise NotAMember("Only the activity owner may add a guardian.")
    if not activity.guardian_accompanied or activity.cohort != Cohort.CHILD:
        raise InvalidState("This activity does not allow accompanying guardians.")
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise NotEligible("A guardian must be a verified adult.")
    if not is_guardian_of(guardian, owner):
        raise NotEligible("This adult is not a registered guardian of the activity owner.")
    existing = (
        activity.memberships.filter(user=guardian).exclude(state=Membership.State.REMOVED).first()
    )
    if existing:
        return existing
    return Membership.objects.create(
        activity=activity,
        user=guardian,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )


@transaction.atomic
@transaction.atomic
def post_to_thread(
    author, activity, body: str, *, reply_to=None, allow_empty=False, ping=False
) -> Post:
    """THE single write path for an activity thread, shared by the web form, the DRF API,
    and the WebSocket consumer (via post_to_thread_realtime). It enforces the FULL union of
    the gates the two old surfaces had — so the child-safety gate holds identically on every
    surface (the whole point of collapsing Post + chat into one stream).

    Gate: current MEMBER (not a supervisory guardian) + verified/consented participation +
    the activity isn't moderator-hidden + the activity isn't CANCELLED (OPEN *and* COMPLETED
    both admit posts, so the post-meetup "thanks for coming" + F22 "did we meet?" flow keep
    working — only a cancelled meetup freezes its thread) + not blocked-vs-owner + a per-user
    rate limit + the swappable MessagePolicy/CSAR content seam. ``reply_to`` is validated to
    the same thread, must not be hidden, and is re-parented to its top-level ancestor so the
    tree can never exceed one level. A committed write schedules a live broadcast on commit.

    ``@mentions`` in the body are always rendered as a calm highlight (tag-not-ping). ``ping``
    is an explicit author opt-in: only then is a MENTION notification fanned out — to PEER
    members named in the body, minus the author and minus blocked pairs, and still mutable by
    each recipient (no engagement-maxxing, no surprise pings)."""
    from apps.chat.policy import get_message_policy  # local: avoid social<->chat import cycle
    from apps.safety.services import allow_action, is_blocked

    membership = thread_members(activity).filter(user=author).first()
    if membership is None:
        raise NotAMember(_("Only current members can post in the activity thread."))
    if membership.role == Membership.Role.GUARDIAN:
        # Guardians accompany children's activities as transparent, read-only supervisors;
        # an adult must not post into a children's thread (cohort isolation for the peers).
        # Vestigial for a Group (GroupMembership has no GUARDIAN role — a test pins this), kept
        # unconditionally so a future GUARDIAN role could never silently gain posting rights.
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    # Minor-cohort GROUP threads are ANNOUNCEMENT-ONLY: peers read, only the owner/staff broadcasts
    # (post_announcement). On a standing, city-wide minor group this collapses the active-poster
    # enumeration surface to zero — the per-meetup Activity thread is where minors actually
    # converse, naturally bounded to that meetup. A no-op for activity threads and adult groups;
    # bylines elsewhere stay so any post author can still be reported.
    if isinstance(activity, Group) and activity.cohort in (Cohort.CHILD, Cohort.TEEN):
        raise NotEligible(
            _("This group's thread is announcement-only; the organiser posts updates here.")
        )
    if not can_participate(author):
        # Catches a member whose parental consent was revoked or assurance lapsed after join.
        raise NotEligible(_("Posting requires verified, consented participation."))
    if getattr(activity, "is_hidden", False):
        raise InvalidState(_("This activity is no longer available."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    if author.id != activity.owner_id and is_blocked(author, activity.owner):
        raise InvalidState(_("This activity is no longer available."))
    limit = getattr(settings, "THREAD_POST_RATE_LIMIT", 30)
    window = getattr(settings, "THREAD_POST_RATE_WINDOW_SECONDS", 60)
    if not allow_action(author, "thread_post", limit=limit, window_seconds=window):
        raise InvalidState(_("You are posting too quickly; slow down."))
    result = get_message_policy().process(author=author, thread=activity.thread, body=body)
    if result.allowed:
        result_body = result.body
    elif allow_empty and not (body or "").strip():
        # An attachment-only message (allow_empty): an empty body is fine. Any OTHER policy
        # rejection (too long, or a future CSAR content block) still applies.
        result_body = ""
    else:
        raise InvalidState(result.reason or _("Message rejected."))
    parent = _validate_reply_to(activity, reply_to)
    post = Post.objects.create(
        thread=activity.thread, author=author, body=result_body, reply_to=parent
    )
    # Normalize updated_at == created_at on a fresh post (auto_now_add and auto_now fire as two
    # separate now() calls, so they'd otherwise differ by microseconds and falsely read as
    # "edited"). After this, any real edit makes updated_at strictly greater. One cheap write.
    Post.objects.filter(pk=post.pk).update(updated_at=post.created_at)
    post.updated_at = post.created_at
    if ping:
        _ping_mentioned(author, activity, post)
    transaction.on_commit(lambda: broadcast_post(post))
    return post


def _ping_mentioned(author, activity, post) -> None:
    """Opt-in fan-out: notify PEER members @mentioned in ``post`` — never the author, never a
    blocked pair, never a guardian (resolve_mentions already excludes guardians by resolving
    against voting_members). notify() still honours each recipient's per-kind mute, so MENTION
    stays a fully user-controllable signal. Runs inside the post's transaction so a rolled-back
    post pings nobody."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids

    # @mentions are an ACTIVITY-thread affordance only. On a standing GROUP thread we deliberately
    # do NOT resolve mentions (no @autocomplete, no ping), so the active-member set is never
    # enumerable by name — consistent with the roster-less-for-minors rule. Bylines remain for
    # reporting; this just removes the name-resolution/ping surface entirely on group threads.
    if isinstance(activity, Group):
        return
    mentioned = resolve_mentions(activity, post.body, exclude_user=author)
    if not mentioned:
        return
    blocked = blocked_user_ids(author)
    who = author.display_name or author.username
    title = _("%(who)s mentioned you") % {"who": who}
    url = f"/activities/{activity.id}/#post-{post.id}"
    for user in mentioned:
        if user.id in blocked:
            continue
        _notify(user, Notification.Kind.MENTION, title, body=post.body[:140], url=url)


def _validate_reply_to(activity, reply_to):
    """Resolve an optional reply target to a TOP-LEVEL ancestor Post in the same thread, or
    None. Re-parenting (parent.reply_to or parent) enforces the one-level depth cap in the
    service, never the schema. Refused: a hidden parent (no replying to a removed post), a
    PINNED ANNOUNCEMENT (it isn't part of the reply tree — a reply to it would be orphaned out
    of thread_page), a wrong-thread parent, and a non-integer id (raised as a domain error, not
    an uncaught ValueError that would tear down the WebSocket consumer)."""
    if reply_to is None:
        return None
    if isinstance(reply_to, Post):
        parent = reply_to
    else:
        try:
            parent = Post.objects.filter(pk=int(reply_to)).first()
        except (TypeError, ValueError) as exc:
            raise InvalidState(_("You can't reply to that message.")) from exc
    if (
        parent is None
        or parent.thread_id != activity.thread.id
        or parent.is_hidden
        or parent.is_announcement
    ):
        raise InvalidState(_("You can't reply to that message."))
    return parent.reply_to if parent.reply_to_id else parent


def post_to_thread_realtime(author, activity, body: str, *, reply_to_id=None) -> Post:
    """Thin wrapper the WebSocket consumer calls so the socket write goes through the EXACT
    same gate as the form/API — gate divergence between surfaces is structurally impossible."""
    return post_to_thread(author, activity, body, reply_to=reply_to_id)


def can_read_thread(user, activity) -> bool:
    """The single read/write membership gate for a thread, used by the web view, the bounded
    history read, AND the WebSocket consumer (connect + per-receive + per-delivery re-auth).
    Folds the old chat.can_access_thread logic so all surfaces agree on who may see a thread.

    ``activity`` is the thread OWNER OBJECT — an Activity or a Group (duck-typed: both expose
    ``is_hidden`` / ``cohort`` / ``memberships`` / ``owner_id`` / ``owner``). Step 3 (the cohort
    wall) is the SINGLE fail-closed read gate; there is no carve-out, so an aged-out or cross-cohort
    user is rejected at read time even if an eviction was missed."""
    if not user or not getattr(user, "is_authenticated", False) or not user.is_active:
        return False
    if getattr(activity, "is_hidden", False):
        return False
    if user.cohort != activity.cohort:
        return False
    if not can_participate(user):
        return False
    if not thread_members(activity).filter(user=user).exists():
        return False
    from apps.safety.services import is_blocked

    if user.id != activity.owner_id and is_blocked(user, activity.owner):
        return False
    return True


def thread_page(activity, *, before=None, limit=None):
    """A bounded, keyset-paginated window of TOP-LEVEL posts (reply_to IS NULL) for an
    activity thread, newest-window-first then returned oldest->newest for display, each with
    its non-hidden replies prefetched (one extra query, no N+1, no recursive CTE). Replaces
    the old unbounded thread load. The CALLER MUST gate on can_read_thread first so the
    ``before`` cursor can never leak across the membership wall. Returns
    (posts_oldest_first, has_older, older_cursor_id)."""
    from django.db.models import Prefetch

    limit = limit or getattr(settings, "SOCIAL_THREAD_POST_LIMIT", 100)
    replies_qs = Post.objects.filter(is_hidden=False).select_related("author", "reply_to__author")
    top = (
        activity.thread.posts.filter(is_hidden=False, is_announcement=False, reply_to__isnull=True)
        .select_related("author")
        .prefetch_related(Prefetch("replies", queryset=replies_qs.order_by("created_at")))
        .order_by("-created_at")
    )
    if before:
        try:
            before_id = int(before)
        except (TypeError, ValueError):
            before_id = None  # a malformed cursor degrades to the first page, never a 500
        anchor = (
            Post.objects.filter(pk=before_id, thread=activity.thread).first()
            if before_id is not None
            else None
        )
        if anchor is not None:
            # Keyset on (created_at, id) — strictly older than the anchor, stable on ties.
            top = top.filter(
                Q(created_at__lt=anchor.created_at)
                | Q(created_at=anchor.created_at, id__lt=anchor.id)
            )
    window = list(top[: limit + 1])
    has_older = len(window) > limit
    window = window[:limit]
    older_cursor_id = window[-1].id if (has_older and window) else None
    window.reverse()  # oldest -> newest for display
    for tp in window:
        tp.is_edited = _is_edited(tp)
        for reply in tp.replies.all():  # prefetched, already filtered + ordered
            reply.is_edited = _is_edited(reply)
            reply.snippet = reply_snippet(reply)
    return window, has_older, older_cursor_id


def _is_edited(post) -> bool:
    # post_to_thread normalizes updated_at == created_at on a fresh post, so a strict
    # inequality means a genuine later edit (edit_post bumps updated_at via auto_now).
    if not post.updated_at or not post.created_at:
        return False
    return post.updated_at > post.created_at


def reply_snippet(post, *, length=120):
    """The 'Replying to <author>: <text>' snippet, ALWAYS derived from the CURRENT parent at
    read time (never a stored copy): a hidden/removed parent yields a neutral placeholder, so
    an edited or moderated parent can't resurface stale text inside its replies."""
    parent = post.reply_to
    if parent is None:
        return None
    author = parent.author.display_name or parent.author.username
    if parent.is_hidden:
        return {"author": author, "text": str(_("(message removed)")), "pk": parent.id}
    text = (parent.body or "").strip().replace("\n", " ")
    if len(text) > length:
        text = text[: length - 1].rstrip() + "…"
    return {"author": author, "text": text, "pk": parent.id}


@transaction.atomic
def edit_post(author, post, body: str) -> Post:
    """Author-only in-place edit. Same participation/status gate as posting; refuses a
    moderator-hidden post (no moderation evasion) and an announcement (the owner re-announces
    instead). Because reply snippets are render-derived, an edit here automatically updates
    every reply that quotes this post on its next read. The 'edited' marker is derived from
    updated_at != created_at — no edit-count, no revision table."""
    from apps.chat.policy import get_message_policy

    if post.author_id != author.id:
        raise NotEligible(_("You can only edit your own messages."))
    if post.is_hidden or post.is_announcement:
        raise InvalidState(_("This message can't be edited."))
    activity = post.thread.owner_object
    if not thread_members(activity).filter(user=author).exists():
        raise NotAMember(_("Only current members can edit a message."))
    if not can_participate(author):
        raise NotEligible(_("Editing requires verified, consented participation."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    result = get_message_policy().process(author=author, thread=post.thread, body=body)
    if not result.allowed:
        raise InvalidState(result.reason or _("Message rejected."))
    post.body = result.body
    post.save(update_fields=["body", "updated_at"])
    transaction.on_commit(lambda: broadcast_post(post, edited=True))
    return post


@transaction.atomic
def delete_own_post(author, post) -> Post:
    """Author soft-delete: flag the post hidden so it drops from member reads but the row is
    RETAINED for audit/appeal (like a moderator REMOVE). Refuses a post already moderator-
    hidden (no clobbering a moderation record). Because snippets are render-derived, a
    self-deleted parent's quote drops from its replies on next read automatically. GDPR
    erasure (apps/ops) stays the only hard-delete path."""
    from apps.safety.services import record_audit

    if post.author_id != author.id:
        raise NotEligible(_("You can only delete your own messages."))
    if post.is_hidden:
        return post  # idempotent; never un-hides a moderation action
    post.is_hidden = True
    post.save(update_fields=["is_hidden", "updated_at"])
    record_audit("post.self_deleted", actor=author, target=post)
    return post


def broadcast_post(post, *, edited=False) -> None:
    """Fan a committed Post out to its thread's WebSocket group as PURE live delivery (the
    durable record already exists; this only saves connected members a reload). Called via
    transaction.on_commit, so a rolled-back write broadcasts nothing. Per-delivery re-auth in
    the consumer drops blocked/cohort-changed/erased members, so this need not filter. Wrapped
    to a graceful no-op when there is no working channel layer (single-process InMemory across
    processes) — the no-JS surface already has the content on reload."""
    try:
        from asgiref.sync import async_to_sync
        from channels.layers import get_channel_layer

        layer = get_channel_layer()
        if layer is None:
            return
        snippet = reply_snippet(post)
        author = post.author.display_name or post.author.username
        payload = {
            "id": post.id,
            "author": author,
            "body": post.body,
            "is_announcement": post.is_announcement,
            "reply_to": post.reply_to_id,
            "reply_snippet": snippet,
            "edited": edited
            or (post.updated_at and post.created_at and post.updated_at > post.created_at),
            "created_at": post.created_at.isoformat() if post.created_at else None,
        }
        async_to_sync(layer.group_send)(
            f"chat_{post.thread_id}", {"type": "chat.message", "message": payload}
        )
    except Exception:  # noqa: BLE001 — live delivery is best-effort; never break the write
        pass


# --- thread reactions (anonymous, COUNTLESS, no who-list) -----------------------------------

# A fixed, NON-extensible ack set — never user-supplied custom emoji (a custom-emoji economy is
# an engagement/vanity surface). Overridable via settings only by an operator.
DEFAULT_REACTION_EMOJIS = ["👍", "❤️", "🎉", "👏", "🙏"]


def allowed_reactions() -> list:
    return list(getattr(settings, "THREAD_REACTION_EMOJIS", DEFAULT_REACTION_EMOJIS))


@transaction.atomic
def toggle_reaction(user, post, emoji) -> bool:
    """Add or remove the user's OWN emoji reaction on a thread post. Enforces the SAME write
    gate as post_to_thread (membership, not-a-guardian, consent, not-blocked-vs-owner, activity
    not hidden/cancelled) plus a fixed-emoji-set and not-a-hidden-post check, so the reaction
    surface can never become a weaker side door than posting. Returns True if now reacted, False
    if removed. Never exposes a count or a who-list anywhere."""
    from apps.safety.services import allow_action, is_blocked

    from .models import PostReaction

    if emoji not in allowed_reactions():
        raise InvalidState(_("That reaction isn't available."))
    if post.is_hidden:
        raise InvalidState(_("You can't react to that message."))
    activity = post.thread.owner_object
    if getattr(activity, "is_hidden", False):
        raise InvalidState(_("This activity is no longer available."))
    membership = thread_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can react."))
    if membership.role == Membership.Role.GUARDIAN:
        # Guardians are read-only supervisors (like post_to_thread) — reacting is a write.
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    if not can_participate(user):
        raise NotEligible(_("Reacting requires verified, consented participation."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    if user.id != activity.owner_id and is_blocked(user, activity.owner):
        # Mirror post_to_thread (a block leaves Membership intact, so it must be re-checked here);
        # otherwise a blocked-vs-owner member's emoji would surface on the owner's own posts.
        raise InvalidState(_("This activity is no longer available."))
    limit = getattr(settings, "THREAD_REACT_RATE_LIMIT", 60)
    window = getattr(settings, "THREAD_REACT_RATE_WINDOW_SECONDS", 60)
    if not allow_action(user, "thread_react", limit=limit, window_seconds=window):
        raise InvalidState(_("You are reacting too quickly; slow down."))
    existing = PostReaction.objects.filter(post=post, user=user, emoji=emoji).first()
    if existing is not None:
        existing.delete()
        return False
    # get_or_create swallows a concurrent duplicate (a fast double-tap) as a benign no-op via its
    # own savepoint, rather than poisoning this atomic block with an unhandled IntegrityError 500.
    # (Don't bind the throwaway to ``_`` — that's the module-level gettext alias.)
    _obj, created = PostReaction.objects.get_or_create(post=post, user=user, emoji=emoji)
    return created


def post_reaction_emojis(post) -> list:
    """The DISTINCT emojis present on a post, in the fixed display order — NO count, NO who."""
    present = set(post.reactions.values_list("emoji", flat=True))
    return [e for e in allowed_reactions() if e in present]


# --- @mentions (tag-not-ping by default; an explicit ping is a calm opt-in) -----------------

# A mention is "@" + a username. The lookbehind refuses a "@" glued to a preceding word char,
# so an email address (alice@example.com) never reads as a mention/ping. Username charset mirrors
# the model's max length; we resolve case-insensitively against ACTUAL peer members, so a token
# that doesn't name a current peer is left as plain text (never a fake highlight, never a ping).
MENTION_RE = re.compile(r"(?<![\w@])@([\w.\-]{1,150})")


def mention_roster(activity) -> dict:
    """Lowercased-username -> peer member User for this activity. PEERS ONLY — a supervisory
    guardian is never mentionable (no adult is pulled into a children's thread by a tag), and a
    mention can never reach outside the activity's own roster. Compute ONCE per request and pass
    into highlight_mentions for each post (the rendering loop must not re-query per post)."""
    out = {}
    for m in voting_members(activity).select_related("user"):
        out[m.user.username.lower()] = m.user
    return out


def resolve_mentions(activity, body, *, exclude_user=None) -> list:
    """Distinct peer members named by '@username' in ``body`` (order of first appearance), minus
    ``exclude_user``. Resolution is against the live roster, so the set self-narrows as people
    leave — a mention is only ever a current peer, never a stranger or a guardian."""
    if not body:
        return []
    roster = mention_roster(activity)
    seen, result = set(), []
    for token in MENTION_RE.findall(body):
        user = roster.get(token.lower())
        if user is None or user.id in seen:
            continue
        if exclude_user is not None and user.id == exclude_user.id:
            continue
        seen.add(user.id)
        result.append(user)
    return result


def highlight_mentions(body, roster):
    """Render a thread body to SAFE HTML: HTML-escaped, newlines -> <br>, and every '@username'
    that names a CURRENT peer member (a key in ``roster`` from ``mention_roster``) wrapped in
    <span class="mention">. Escaping happens BEFORE any markup is inserted, so a hostile body can
    never inject HTML; only the literal mention spans we add are trusted. A token that doesn't
    resolve to a peer stays plain escaped text."""
    from django.utils.html import escape
    from django.utils.safestring import mark_safe

    if not body:
        return mark_safe("")

    def repl(match):
        token = match.group(1)
        if token.lower() in roster:
            return f'<span class="mention">@{escape(token)}</span>'
        return escape(match.group(0))  # not a real member — leave as escaped plain text

    # Escape the gaps between mentions ourselves so the whole string is safe, then mark_safe.
    pieces, last = [], 0
    for m in MENTION_RE.finditer(body):
        pieces.append(escape(body[last : m.start()]))
        pieces.append(repl(m))
        last = m.end()
    pieces.append(escape(body[last:]))
    # Normalise CRLF/CR before turning newlines into <br> so no stray \r survives (parity with
    # the |linebreaksbr fallback). All segments are already escaped, so this only adds our <br>.
    html = "".join(pieces).replace("\r\n", "\n").replace("\r", "\n").replace("\n", "<br>")
    return mark_safe(html)


def reactions_for_posts(posts, viewer) -> dict:
    """Batch (no N+1): post_id -> {"present": [distinct emojis, no count], "mine": {viewer's own}}.
    Used by the thread view to render reaction chips + highlight the viewer's own toggles."""
    from .models import PostReaction

    ids = [p.id for p in posts]
    out = {pid: {"present": set(), "mine": set()} for pid in ids}
    if not ids:
        return out
    for r in PostReaction.objects.filter(post_id__in=ids).values("post_id", "user_id", "emoji"):
        slot = out[r["post_id"]]
        slot["present"].add(r["emoji"])
        if r["user_id"] == viewer.id:
            slot["mine"].add(r["emoji"])
    order = allowed_reactions()
    return {
        pid: {
            "present": [e for e in order if e in v["present"]],  # ordered distinct, no count
            "mine": v["mine"],
        }
        for pid, v in out.items()
    }


@transaction.atomic
def post_announcement(owner, activity, body: str) -> Post:
    """Owner-only pinned broadcast: a must-read logistics post that surfaces above the
    thread and fires one notification to every current member. Same cohort/consent gate
    as an ordinary post; only the owner may use it."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids

    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the organiser can post an announcement."))
    if not can_participate(owner):
        raise NotEligible(_("Posting requires verified, consented participation."))
    # Re-check current membership (mirrors post_to_thread's first gate): an owner who was EVICTED
    # (e.g. a cohort change flipped their membership to REMOVED) can no longer broadcast into a
    # group they no longer belong to, even though they still nominally own the row. Deliberately
    # NOT a cohort check — the staff curator of a MINOR group is an ADULT and MUST still announce.
    if thread_members(activity).filter(user=owner).first() is None:
        raise NotAMember(_("Only a current member can post an announcement."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    post = Post.objects.create(
        thread=activity.thread, author=owner, body=body, is_announcement=True
    )
    body_preview = body.strip()
    if len(body_preview) > 140:
        body_preview = body_preview[:139].rstrip() + "…"
    # An announcement on a GROUP is the owner/staff broadcast channel (and, for a minor group, the
    # ONLY write into its announcement-only thread). It uses the mutable GROUP_ANNOUNCEMENT kind and
    # a /groups/ link; an activity announcement keeps its existing kind + link unchanged.
    is_group = isinstance(activity, Group)
    kind = Notification.Kind.GROUP_ANNOUNCEMENT if is_group else "announcement"
    url = f"/groups/{activity.id}/" if is_group else f"/api/social/activities/{activity.id}/"
    title = (_("Group announcement: %(title)s") if is_group else _("Announcement: %(title)s")) % {
        "title": activity.title
    }
    # Exclude blocked pairs from the fan-out — without this a member who blocked (or was
    # blocked by) the owner kept receiving the owner's announcements (the pre-existing gap
    # that mark_arrived already closes). The live group_send is filtered at delivery by the
    # consumer's can_read_thread re-auth, which also drops blocked members. The fan-out targets
    # current MEMBERS of the (cohort-pinned) thread and does not re-check each recipient's live
    # cohort/eligibility — matching every other notify() fan-out; can_read_thread re-gates at READ
    # time, so a stale recipient at most sees a notification title, never thread content (LOW).
    blocked = blocked_user_ids(owner)
    recipients = (
        thread_members(activity)
        .exclude(user_id=owner.id)
        .exclude(user_id__in=blocked)
        .select_related("user")
    )
    for membership in recipients:
        _notify(membership.user, kind, title, body=body_preview, url=url)
    transaction.on_commit(lambda: broadcast_post(post))
    return post


# --- F20: RSVP attendance intent -------------------------------------------------------


@transaction.atomic
def set_attendance_intent(user, activity, intent) -> Membership:
    """A current member flips their transient go/no-go for THIS activity. No notification,
    no audit, no cross-activity history (that would be behavioural tracking)."""
    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can RSVP."))
    if intent not in Membership.AttendanceIntent.values:
        raise InvalidState(_("Invalid attendance choice."))
    membership.attendance_intent = intent
    membership.save(update_fields=["attendance_intent", "updated_at"])
    _maybe_confirm_meetup(activity)  # F1: one-shot "it's on" notice when GOING crosses min_to_go
    return membership


def _maybe_confirm_meetup(activity) -> None:
    """F1 Quorum-go: if a min_to_go threshold is set and the LIVE GOING count has now reached it,
    latch the one-shot ``go_confirmed_at`` and fan out a single MEETUP_CONFIRMED notice. The latch
    ONLY dedups the notification (so a wobbling count never spams) — it never feeds the displayed
    state. Locks the activity row so two concurrent RSVPs can't double-fire the notice. Must be
    called inside set_attendance_intent's transaction (so the just-saved RSVP is counted)."""
    if activity.min_to_go is None:
        return
    locked = Activity.objects.select_for_update().get(pk=activity.pk)
    if locked.min_to_go is None or locked.go_confirmed_at is not None:
        return
    if locked.status != Activity.Status.OPEN:
        # A cancelled/completed meetup can never become "on" — never latch or fan out an
        # "it's happening" notice for a frozen activity (checked on the locked row, race-safe).
        return
    going = (
        voting_members(locked).filter(attendance_intent=Membership.AttendanceIntent.GOING).count()
    )
    if going < locked.min_to_go:
        return
    locked.go_confirmed_at = timezone.now()
    locked.save(update_fields=["go_confirmed_at", "updated_at"])
    activity.go_confirmed_at = locked.go_confirmed_at  # keep the caller's instance fresh
    # Notify synchronously in-txn (like mark_arrived / post_announcement create their notices): a
    # rolled-back RSVP rolls back the latch AND the notices together, so a phantom "it's on" can
    # never outlive a failed RSVP. The activity-row lock above makes the one-shot fan-out atomic.
    _notify_meetup_confirmed(locked)


def _notify_meetup_confirmed(activity) -> None:
    """Fan a single 'the meetup is on' notice to current members (minus blocked pairs), once the
    quorum is first reached. Mirrors post_announcement's blocked-pair exclusion."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids

    blocked = blocked_user_ids(activity.owner)
    title = _("A meetup is on")
    body = _("“%(title)s” has enough people going — it's happening.") % {"title": activity.title}
    url = f"/api/social/activities/{activity.id}/"
    for membership in current_members(activity).exclude(user_id__in=blocked).select_related("user"):
        _notify(membership.user, Notification.Kind.MEETUP_CONFIRMED, title, body=body, url=url)


def attendance_summary(activity) -> dict:
    """Per-activity go count for the participants (peers, excluding supervisory guardians), plus the
    F1 Quorum-go state. A live snapshot shown only to members — never stored, never aggregated
    per-user. ``met_minimum`` / ``remaining_needed`` are derived LIVE from the current GOING count
    (NOT from the go_confirmed_at latch), so the chip can never claim "on" after the count drops."""
    members = voting_members(activity)
    going = members.filter(attendance_intent=Membership.AttendanceIntent.GOING).count()
    # The forward-looking quorum state ("it's on / needs N more") is meaningful only for a LIVE
    # (OPEN) meetup — a cancelled or completed activity has no such state, and showing one would be
    # a lying chip. The configured min_to_go still lives on the model + ActivitySerializer; this
    # dict is the LIVE snapshot, so its quorum keys go None once the meetup is no longer open.
    live = activity.min_to_go is not None and activity.status == Activity.Status.OPEN
    min_to_go = activity.min_to_go if live else None
    return {
        "going": going,
        "total": members.count(),
        "min_to_go": min_to_go,
        "met_minimum": (going >= min_to_go) if live else None,
        "remaining_needed": max(min_to_go - going, 0) if live else None,
    }


@transaction.atomic
def set_met_confirmed(user, activity, confirmed: bool = True) -> Membership:
    """A participant privately confirms (or undoes) that a finished meetup actually happened
    (F22). Allowed only once the activity is COMPLETED. No notification, no audit, no
    cross-activity trace — it is a single per-activity boolean, never a judgement of a person."""
    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can confirm a meetup."))
    if membership.role == Membership.Role.GUARDIAN:
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    if activity.status != Activity.Status.COMPLETED:
        raise InvalidState(_("You can only confirm a meetup after it has finished."))
    if confirmed and membership.met_confirmed_at is not None:
        return membership  # idempotent: a second tap changes nothing
    membership.met_confirmed_at = timezone.now() if confirmed else None
    membership.save(update_fields=["met_confirmed_at", "updated_at"])
    return membership


def met_confirmation_summary(activity) -> dict:
    """Per-activity 'did we meet up?' count over the participants (excludes guardians). A live
    snapshot shown only to members — never stored, never rolled up per-user or cross-activity."""
    members = voting_members(activity)
    return {
        "confirmed": members.filter(met_confirmed_at__isnull=False).count(),
        "total": members.count(),
    }


# --- F35: extractive "catch up" thread digest -----------------------------------------


def thread_digest(activity, viewer) -> dict:
    """A deterministic, extractive recap of an activity thread (F35): the latest announcements, a
    few logistical posts (conservative keyword match) and the most-recent posts. Pure read; the
    SAME content for every member (no per-user 'last read' state — that would be behavioural
    tracking). Bounded by DIGEST_SCAN_LIMIT.

    The numeric summary (going / total / member_count) is COHORT-GATED by ``viewer``: it is returned
    only to an ADULT viewer, and is None for CHILD/TEEN viewers (and anyone else). ``member_count``
    AND ``total`` both reveal the roster size, so the headline 'minors never see a member count'
    rule suppresses the whole numeric block for minors — the digest then carries only the textual
    content. ``viewer`` is REQUIRED so a caller can never accidentally emit an ungated count."""
    posts = activity.thread.posts
    announcements = list(
        posts.filter(is_hidden=False, is_announcement=True)
        .select_related("author")
        .order_by("-created_at")[:DIGEST_MAX_ANNOUNCEMENTS]
    )
    scanned = list(
        posts.filter(is_hidden=False, is_announcement=False)
        .select_related("author")
        .order_by("-created_at")[:DIGEST_SCAN_LIMIT]
    )
    recent = scanned[:DIGEST_RECENT_POSTS]
    recent_ids = {p.id for p in recent}
    logistical = [p for p in scanned if p.id not in recent_ids and _LOGISTICAL_RE.search(p.body)][
        :DIGEST_LOGISTICAL_POSTS
    ]
    # Counts are an ADULT-only surface (the count-leak fix, platform-wide). Skip the queries
    # entirely for a minor/anon viewer so nothing leaks and nothing is wasted.
    going = total = member_count = None
    if getattr(viewer, "cohort", None) == Cohort.ADULT:
        att = attendance_summary(activity)
        going, total = att["going"], att["total"]
        member_count = current_members(activity).count()
    return {
        "announcements": announcements,
        "recent": recent,
        "logistical": logistical,
        "going": going,
        "total": total,
        "member_count": member_count,
        "has_content": bool(announcements or recent or logistical),
    }


# --- F36: template-driven activity draft helper ----------------------------------------


def draft_activity_text(*, activity_type, place=None, starts_at=None, cohort=None) -> dict:
    """A deterministic (no ML) draft title + description composed from the organiser's OWN
    chosen type/place/time, to seed an empty create form (F36). A CHILD/TEEN organiser also
    gets a short safety reminder. Returns {'title', 'description'}; callers only ever seed
    EMPTY initial, never overwrite what the user typed. gettext fragments are str()-coerced
    before slicing/concatenation (a lazy proxy can't be sliced)."""
    has_place_name = bool(place and (place.name or "").strip())
    if has_place_name:
        title = str(_("%(type)s at %(place)s") % {"type": activity_type.name, "place": place.name})
    else:
        title = str(activity_type.name)
    title = title[:200]

    where = str(_(" at %(place)s") % {"place": place.name}) if has_place_name else ""
    when = str(_(" on %(when)s") % {"when": f"{starts_at:%a %d %b, %H:%M}"}) if starts_at else ""
    base = str(
        _("A %(type)s meetup%(where)s%(when)s. Add any details below before you post.")
        % {"type": activity_type.name, "where": where, "when": when}
    )
    # Minor signal = cohort, NOT requires_parental_consent (which is UNDER_16-only and would
    # silently skip TEEN organisers).
    if cohort in (Cohort.CHILD, Cohort.TEEN):
        safety = str(_("Safety: meet in a public place and bring a friend."))
        description = "\n\n".join([base, safety])
    else:
        description = base
    return {"title": title, "description": description}


# --- F3: "we're here" arrival ping -----------------------------------------------------


def arrival_window_open(activity) -> bool:
    """Whether arrival may be marked right now: an OPEN activity within the start-relative
    window. Used by the web view to show/hide the button (the service re-checks anyway)."""
    if activity.status != Activity.Status.OPEN:
        return False
    now = timezone.now()
    before = getattr(settings, "ARRIVAL_WINDOW_BEFORE_HOURS", ARRIVAL_WINDOW_BEFORE_HOURS)
    after = getattr(settings, "ARRIVAL_WINDOW_AFTER_HOURS", ARRIVAL_WINDOW_AFTER_HOURS)
    return (
        activity.starts_at - timedelta(hours=before)
        <= now
        <= activity.starts_at + timedelta(hours=after)
    )


@transaction.atomic
def mark_arrived(user, activity) -> Membership:
    """A current member self-declares "I've arrived". Quietly tells the OTHER current
    members (excluding blocked pairs); for a CHILD-cohort member it ALSO tells their active
    guardian(s), so a child is never standing alone. Self-declared only (no on-behalf-of),
    no free text, no location ever, idempotent, and cleared a few hours later by
    expire_arrivals so it never becomes a presence dashboard."""
    from apps.safety.services import blocked_user_ids, record_audit

    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can mark themselves arrived."))
    if not can_participate(user):
        raise NotEligible(_("Marking arrival requires verified, consented participation."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("You can only mark arrival for an active meetup."))
    if not arrival_window_open(activity):
        raise InvalidState(_("Arrival can only be marked around the start time."))
    if membership.arrived_at is not None:
        return membership  # idempotent: a second tap never re-pings the group

    membership.arrived_at = timezone.now()
    membership.save(update_fields=["arrived_at", "updated_at"])

    blocked = blocked_user_ids(user)
    # Server-composed, fixed copy. The only arriver-derived string is display_name — the
    # same low-entropy handle already shown app-wide (members list, thread). NO per-ping
    # note exists, so no unmoderated child-authored text reaches an adult.
    title = _("Someone arrived")
    body = _("%(name)s is at “%(title)s”.") % {
        "name": user.display_name or user.username,
        "title": activity.title,
    }
    url = f"/api/social/activities/{activity.id}/"
    notified: set[int] = set()
    for member in current_members(activity).exclude(user_id=user.id).select_related("user"):
        if member.user_id in blocked:
            continue
        _notify(member.user, "arrival", title, body=body, url=url)
        notified.add(member.user_id)

    # CHILD cohort only (teens self-manage, matching F5/F6). Keyed on an ACTIVE
    # GuardianRelationship — never a loose is_child flag. Each guardian gets at most one ping.
    if user.cohort == Cohort.CHILD:
        for rel in GuardianRelationship.objects.filter(
            ward=user, status=GuardianRelationship.Status.ACTIVE
        ).select_related("guardian"):
            guardian = rel.guardian
            if guardian.id in blocked or guardian.id in notified:
                continue
            _notify(guardian, "arrival", title, body=body, url=url)
            notified.add(guardian.id)

    record_audit("activity.arrived", actor=user, target=activity)
    return membership


@transaction.atomic
def propose_place(proposer, place, required_confirmations=None) -> UserPlaceProposal:
    if not can_participate(proposer):
        raise NotEligible("User cannot propose places (needs verification/consent).")
    return UserPlaceProposal.objects.create(
        place=place,
        proposer=proposer,
        required_confirmations=(
            DEFAULT_PLACE_QUORUM if required_confirmations is None else required_confirmations
        ),
    )


@transaction.atomic
def confirm_place(user, proposal: UserPlaceProposal) -> UserPlaceProposal:
    if proposal.status != UserPlaceProposal.Status.PENDING:
        raise InvalidState("This place proposal is no longer open for confirmation.")
    if proposal.proposer_id == user.id:
        raise InvalidState("The proposer cannot confirm their own place.")
    if not can_participate(user):
        raise NotEligible("User cannot confirm places (needs verification/consent).")
    PlaceConfirmation.objects.get_or_create(proposal=proposal, user=user)
    if proposal.confirmations.count() >= proposal.required_confirmations:
        proposal.status = UserPlaceProposal.Status.PUBLISHED
        proposal.published_at = timezone.now()
        proposal.save(update_fields=["status", "published_at"])
    return proposal


@transaction.atomic
def propose_place_with_venue(
    proposer, *, name, lon, lat, activity_type, allow_nearby=False
) -> UserPlaceProposal:
    """Create a user venue (source=USER) + its seed activity edge, then open a co-creation
    proposal (F25). Hidden from the public until the quorum (or staff) publishes it. A
    name-similar venue within the dedup radius is a hard DuplicatePlace; any place within the
    soft radius is a DuplicatePlace(soft=True) the proposer can override with allow_nearby."""
    from django.contrib.gis.geos import Point
    from django.contrib.gis.measure import D

    from apps.places.enrichment.dedup import find_duplicate
    from apps.places.models import Place, PlaceActivity

    if not can_participate(proposer):  # fail before creating any Place (no orphan)
        raise NotEligible(_("User cannot propose places (needs verification/consent)."))
    name = (name or "").strip()[:255]
    point = Point(lon, lat, srid=4326)
    radius = getattr(settings, "PLACE_PROPOSAL_DEDUP_RADIUS_M", PLACE_PROPOSAL_DEDUP_RADIUS_M)
    dup = find_duplicate(point, name, max_distance_m=radius)
    if dup is not None:  # same named venue nearby — hard block
        raise DuplicatePlace(dup.id, dup.name)
    if not allow_nearby:  # soft: any place very close, even with a different name
        soft_radius = getattr(
            settings, "PLACE_PROPOSAL_SOFT_RADIUS_M", PLACE_PROPOSAL_SOFT_RADIUS_M
        )
        near = Place.objects.filter(location__distance_lte=(point, D(m=soft_radius))).first()
        if near is not None:
            raise DuplicatePlace(near.id, near.name or "a nearby place", soft=True)
    place = Place.objects.create(name=name, location=point, source=Place.Source.USER)
    # origin=MANUAL is in the ingest PROTECTED_ORIGINS, so a re-ingest won't clobber the edge.
    PlaceActivity.objects.create(
        place=place,
        activity=activity_type,
        origin=PlaceActivity.Origin.MANUAL,
        confidence=1.0,
        source="user",
    )
    return propose_place(proposer, place)


@transaction.atomic
def staff_publish_proposal(staff_user, proposal: UserPlaceProposal) -> UserPlaceProposal:
    """Moderator fast-publish (F25) — the single-launch-city escape hatch when a 3-user quorum
    won't be reached organically."""
    if not staff_user.is_staff:
        raise NotEligible(_("Only staff may publish a place proposal."))
    if proposal.status != UserPlaceProposal.Status.PENDING:
        raise InvalidState(_("This proposal is not pending."))
    proposal.status = UserPlaceProposal.Status.PUBLISHED
    proposal.published_at = timezone.now()
    proposal.save(update_fields=["status", "published_at"])
    from apps.safety.services import record_audit

    record_audit("place.staff_published", actor=staff_user, target=proposal.place)
    return proposal


@transaction.atomic
def staff_reject_proposal(
    staff_user, proposal: UserPlaceProposal, *, reason=""
) -> UserPlaceProposal:
    """Moderator close-out of a bad/duplicate submission. A REJECTED proposal keeps its place
    hidden by public_places (never published)."""
    if not staff_user.is_staff:
        raise NotEligible(_("Only staff may reject a place proposal."))
    if proposal.status != UserPlaceProposal.Status.PENDING:
        raise InvalidState(_("This proposal is not pending."))
    proposal.status = UserPlaceProposal.Status.REJECTED
    proposal.save(update_fields=["status"])
    from apps.safety.services import record_audit

    record_audit(
        "place.staff_rejected", actor=staff_user, target=proposal.place, reason=reason[:200]
    )
    return proposal


def pending_proposals_for(user):
    """Open proposals OTHER users may confirm (F25). Annotates a confirmation count so the list
    can show '2 of 3 confirmed' WITHOUT ever naming the proposer/confirmers."""
    from django.db.models import Count

    return (
        UserPlaceProposal.objects.filter(status=UserPlaceProposal.Status.PENDING)
        .exclude(proposer=user)
        .select_related("place")
        .annotate(confirmations_count=Count("confirmations"))
        .order_by("created_at")[:200]
    )


# --- Public Groups: persistent, cohort-pinned standing groups ------------------------------
#
# A Group reuses the ONE hardened thread stack (post_to_thread / can_read_thread, generalised to
# owner_object above) rather than cloning it. The group-specific logic below is discovery (who can
# see which groups), membership (open join / leave / cohort-change eviction), the per-cohort roster
# rule (minors are roster-LESS), creation/curation (staff-only for minors; flag-gated for adults),
# and the read-time Community linkage. Every state-changing service is @transaction.atomic and
# audits inside the txn. See docs/PUBLIC_GROUPS_DESIGN.md.


def _group_user_creation_cohorts() -> set:
    """Cohorts permitted to SELF-CREATE a group (the hard-wall). UNASSIGNED and BOTH minor cohorts
    are unconditionally discarded — a minor can never own/create a group even by misconfiguration
    (mirrors connections._allowed_cohorts). Minor groups are staff-curated only, never self-made."""
    allowed = set(getattr(settings, "GROUPS_USER_CREATION_COHORTS", (Cohort.ADULT,)))
    allowed.discard(Cohort.UNASSIGNED)
    allowed.discard(Cohort.CHILD)
    allowed.discard(Cohort.TEEN)
    return allowed


def visible_groups(viewer):
    """Groups a viewer may discover — ACTIVE, not-hidden groups of their OWN cohort, excluding any
    owned by a blocked user. The SOLE read-access path for group discovery: every group-entity
    surface (web list/detail, DRF GroupViewSet, Community linkage) MUST source from this, so a CHILD
    can never even learn an ADULT group exists. Anon/UNASSIGNED -> none (never a named empty group),
    mirroring visible_communities."""
    if not getattr(viewer, "is_authenticated", False) or viewer.cohort == Cohort.UNASSIGNED:
        return Group.objects.none()
    from apps.safety.services import blocked_user_ids

    qs = Group.objects.filter(
        cohort=viewer.cohort, status=Group.Status.ACTIVE, is_hidden=False
    ).select_related("area", "category", "activity_type", "owner")
    blocked = blocked_user_ids(viewer)
    if blocked:
        qs = qs.exclude(owner_id__in=blocked)
    return qs.order_by("title")


def group_by_id(group_id, viewer):
    """A group at this id reachable by the viewer, else None — a cross-cohort/hidden/archived id is
    a clean 404 for an ordinary member, never a content leak. The single RETRIEVE chokepoint shared
    by the web detail/management views and the DRF GroupViewSet retrieve.

    STAFF get a curation/moderation bypass (any group, incl. hidden/cross-cohort), mirroring the
    activity_detail staff bypass — so the adult staff curator of a MINOR group can still
    view/announce/archive it (announce + archive are owner/staff-gated, not cohort-gated). The
    cohort wall in can_read_thread still bars them from a minor PEER thread (announcement-only
    anyway). DISCOVERY (visible_groups + the list) stays STRICTLY cohort-walled; this bypass is
    read-only retrieve, never a discovery surface."""
    if getattr(viewer, "is_staff", False):
        return Group.objects.filter(pk=group_id).first()
    return visible_groups(viewer).filter(pk=group_id).first()


def group_feed_activities(group, viewer, *, upcoming=True):
    """Upcoming activities a viewer can see in this group's (area x type/category) coordinate — the
    same cohort-filtered feed a Community shows, narrowed to the group's coordinate. Cohort-walled
    twice (viewer.cohort == group.cohort here, and visible_activities is itself cohort-pinned). This
    is DISCOVERY (activities to go to), never the group's membership."""
    from apps.communities.services import _area_place_q

    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != group.cohort:
        return Activity.objects.none()
    qs = visible_activities(viewer).filter(_area_place_q(group.area))
    if group.tier == Group.Tier.TYPE:
        qs = qs.filter(activity_type=group.activity_type)
    else:
        qs = qs.filter(activity_type__category=group.category)
    if upcoming:
        qs = qs.filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
    return qs.select_related("activity_type", "place", "owner").order_by("starts_at")


@transaction.atomic
def create_group(
    actor,
    *,
    area,
    title,
    activity_type=None,
    category=None,
    description="",
    cohort=None,
    is_staff_curated=False,
):
    """Create a standing group. The cohort is PINNED at creation and IMMUTABLE thereafter; cross-age
    is structurally impossible. Two creation paths:

      - CHILD/TEEN group: STAFF-CURATED ONLY (``actor.is_staff``) and only while minor onboarding is
        enabled (the whole minor apparatus ships dark in prod). A minor can never own a group — an
        openly-joinable, persistent space gathering minors by city+activity is a high-value grooming
        target, so a human gate guards its very existence (matching how minor Communities are
        materialized by a vetted job, never user-declared).
      - ADULT group: self-creatable only behind ``GROUPS_ALLOW_USER_CREATED`` (default False —
        staff-curated everywhere first); staff may always create.

    A non-staff actor can only ever create a group in their OWN cohort. Owner is auto-admitted
    (role=OWNER, state=MEMBER); the Thread is created in the SAME txn (mirrors create_activity)."""
    from apps.accounts.services import minor_onboarding_enabled
    from apps.safety.services import allow_action, record_audit

    if not can_participate(actor) or not _has_cohort(actor):
        raise NotEligible(
            _("You need verified, consented participation and a cohort to create a group.")
        )

    target_cohort = cohort or actor.cohort
    if target_cohort == Cohort.UNASSIGNED:
        raise NotEligible(_("Group creation is not available for your account."))
    # A non-staff actor can only ever create a group in their OWN cohort (no cross-cohort creation).
    if not actor.is_staff and target_cohort != actor.cohort:
        raise NotEligible(_("You can only create a group in your own cohort."))

    minor = target_cohort in (Cohort.CHILD, Cohort.TEEN)
    if minor:
        if not actor.is_staff:
            raise NotEligible(_("Groups for under-18s are created by staff only."))
        if not minor_onboarding_enabled():
            raise NotEligible(_("Minor groups are not enabled on this deployment."))
        is_staff_curated = True
    else:
        # Adult-cohort. The hard-wall makes CHILD/TEEN structurally unreachable here regardless.
        if target_cohort not in _group_user_creation_cohorts():
            raise NotEligible(_("Group creation is not available for that cohort."))
        if not actor.is_staff and not getattr(settings, "GROUPS_ALLOW_USER_CREATED", False):
            raise NotEligible(_("Group creation is staff-only on this deployment for now."))
        if actor.is_staff:
            is_staff_curated = True

    # Resolve the taxonomy coordinate -> tier + rollup category.
    if activity_type is not None:
        tier = Group.Tier.TYPE
        category = activity_type.category
    elif category is not None:
        tier = Group.Tier.CATEGORY
    else:
        raise InvalidState(_("A group needs an activity type or a category."))

    limit = getattr(settings, "GROUP_CREATE_RATE_LIMIT", 5)
    window = getattr(settings, "GROUP_CREATE_RATE_WINDOW_SECONDS", 3600)
    if not allow_action(actor, "group_create", limit=limit, window_seconds=window):
        raise InvalidState(_("You are creating groups too quickly; slow down."))

    group = Group.objects.create(
        owner=actor,
        area=area,
        category=category,
        activity_type=activity_type,
        tier=tier,
        cohort=target_cohort,
        title=title,
        description=description,
        is_staff_curated=is_staff_curated,
    )
    GroupMembership.objects.create(
        group=group,
        user=actor,
        role=GroupMembership.Role.OWNER,
        state=GroupMembership.State.MEMBER,
    )
    Thread.objects.create(group=group)
    record_audit(
        "group.created",
        actor=actor,
        target=group,
        cohort=str(target_cohort),
        staff_curated=is_staff_curated,
    )
    return group


@transaction.atomic
def join_group(user, group_id) -> GroupMembership:
    """Openly join a group: admit straight to MEMBER (no vote, no request). Re-resolves the group
    through visible_groups(user) by id (NEVER trusts a passed object), so a hidden/archived/
    cross-cohort group can't be joined. Idempotent — a re-join by a current member is a no-op (never
    re-notifies, never re-audits). Rate-limited on a DEDICATED bucket (mass-join reconnaissance)."""
    from apps.safety.services import allow_action, is_blocked, record_audit

    group = visible_groups(user).filter(pk=group_id).first()
    if group is None:
        raise NotAMember(_("No such group."))
    if not can_participate(user):
        raise NotEligible(_("Joining requires verified, consented participation."))
    if user.id != group.owner_id and is_blocked(user, group.owner):
        raise InvalidState(_("This group is no longer available."))
    existing = group.memberships.filter(user=user).first()
    if existing is not None and existing.state == GroupMembership.State.MEMBER:
        return existing  # idempotent: already a member
    limit = getattr(settings, "GROUP_JOIN_RATE_LIMIT", 20)
    window = getattr(settings, "GROUP_JOIN_RATE_WINDOW_SECONDS", 3600)
    if not allow_action(user, "group_join", limit=limit, window_seconds=window):
        raise InvalidState(_("You are joining groups too quickly; slow down."))
    if existing is not None:
        existing.state = GroupMembership.State.MEMBER
        existing.save(update_fields=["state"])
        membership = existing
    else:
        membership = GroupMembership.objects.create(
            group=group,
            user=user,
            role=GroupMembership.Role.MEMBER,
            state=GroupMembership.State.MEMBER,
        )
    record_audit("group.joined", actor=user, target=group)
    return membership


@transaction.atomic
def leave_group(user, group_id) -> GroupMembership | None:
    """A member leaves a group (state -> LEFT). The owner cannot leave (they archive instead).
    Re-resolves via visible_groups. Returns the membership, or None if not a current member."""
    from apps.safety.services import record_audit

    group = visible_groups(user).filter(pk=group_id).first()
    if group is None:
        return None
    membership = group.memberships.filter(user=user).first()
    if membership is None or membership.state != GroupMembership.State.MEMBER:
        return None
    if membership.role == GroupMembership.Role.OWNER:
        raise InvalidState(_("The owner cannot leave their own group; archive it instead."))
    membership.state = GroupMembership.State.LEFT
    membership.save(update_fields=["state"])
    record_audit("group.left", actor=user, target=group)
    return membership


@transaction.atomic
def remove_user_from_groups(user, *, reason="participation_revoked") -> int:
    """Evict a user from ALL their groups (MEMBER -> REMOVED) when they lose eligibility — a cohort
    change (their old groups are all cross-cohort now) or a consent/participation revocation (which
    does NOT change cohort, so it must be wired separately — see accounts.apply_assurance /
    revoke_parental_consent / revoke_guardian). Mirrors messaging.remove_user_from_conversations but
    SIMPLER: groups have no guardian-observer rows, so there is no prune fan-out. The read-time
    cohort + can_participate re-checks in can_read_thread / group_roster fail closed even if a call
    here is somehow missed. Returns the number of memberships removed."""
    from apps.safety.services import record_audit

    n = GroupMembership.objects.filter(user=user, state=GroupMembership.State.MEMBER).update(
        state=GroupMembership.State.REMOVED
    )
    # ACCEPTED LOW (review finding): this evicts the user's own MEMBERSHIPS but does not archive
    # groups they OWN. An evicted owner can no longer announce (post_announcement re-checks current
    # membership) or peer-post, and staff can archive an ownerless group, so at most a benign
    # unmanaged SAME-cohort group remains — never a cross-cohort or safety hole. Minor groups are
    # staff-owned and staff are never evicted here, so a minor group can never become ownerless.
    if n:
        record_audit("group.participation_revoked", actor=user, count=n, reason=reason)
    return n


def group_roster(group, viewer):
    """The SOLE 'who is in this group' read path — returns a list[User] or None. Cohort wall FIRST,
    then the per-cohort rule (the headline child-safety requirement):

      - anon / cross-cohort viewer    -> None
      - CHILD / TEEN viewer           -> None ALWAYS (minors never see a roster/count/who-is-here)
      - ADULT non-member              -> None (member-gated, not just cohort-gated)
      - ADULT member                  -> the live members, defended in depth: each listed member is
        re-filtered to ``user.cohort == group.cohort`` (a missed eviction can NEVER surface an
        off-cohort user — symmetric with can_read_thread's read-time cohort re-check), to active +
        eligible (can_participate, catching a consent-revoked/suspended member eviction missed), and
        block-filtered both ways."""
    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != group.cohort:
        return None
    if viewer.cohort in (Cohort.CHILD, Cohort.TEEN):
        return None
    if not group.memberships.filter(user=viewer, state=GroupMembership.State.MEMBER).exists():
        return None
    from apps.safety.services import blocked_user_ids

    blocked = blocked_user_ids(viewer)
    rows = (
        group.memberships.filter(
            state=GroupMembership.State.MEMBER, user__cohort=group.cohort, user__is_active=True
        )
        .exclude(user_id__in=blocked)
        .select_related("user")
        .order_by("joined_at")
    )
    return [m.user for m in rows if can_participate(m.user)]


def group_member_count(group, viewer):
    """``len(group_roster(...))`` or None — the SAME gated read, never a stored count, never a
    second surface. None for minors / non-members; an incidental display number for an adult
    member."""
    roster = group_roster(group, viewer)
    return len(roster) if roster is not None else None


@transaction.atomic
def archive_group(actor, group) -> Group:
    """Owner or staff archives a group: status -> ARCHIVED, which FREEZES its thread (via
    is_thread_frozen) and drops it from discovery (visible_groups filters ACTIVE). Not a hard
    delete (audit/appeal)."""
    from apps.safety.services import record_audit

    if actor.id != group.owner_id and not getattr(actor, "is_staff", False):
        raise NotAMember(_("Only the owner or staff can archive a group."))
    if group.status != Group.Status.ACTIVE:
        return group
    group.status = Group.Status.ARCHIVED
    group.save(update_fields=["status", "updated_at"])
    record_audit("group.archived", actor=actor, target=group)
    return group


def linked_group_for_community(community, viewer):
    """The ACTIVE group (if any) on the SAME (cohort, area, type-or-category) coordinate as a
    community, VISIBLE to the viewer. Sourced from visible_groups(viewer) — NEVER raw Group.objects
    — so a child community card can never surface an adult group's existence (both ends cohort-
    walled). Returns a Group or None; the caller links by NAME only (no membership/count)."""
    qs = visible_groups(viewer).filter(area_id=community.area_id)
    if community.tier == community.Tier.TYPE:
        qs = qs.filter(tier=Group.Tier.TYPE, activity_type_id=community.activity_type_id)
    else:
        qs = qs.filter(tier=Group.Tier.CATEGORY, category_id=community.category_id)
    return qs.first()
