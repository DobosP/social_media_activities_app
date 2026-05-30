"""Domain logic for the social core: cohort-gated activities, join-by-vote, and the
user-place quorum. Views and admin go through these functions so the safety
invariants (cohort isolation, verified-and-consented participation) live in one place.
"""

from django.db import transaction
from django.db.models import Count, Q
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from apps.accounts.models import Cohort
from apps.accounts.services import can_participate

from .models import (
    DEFAULT_JOIN_THRESHOLD,
    DEFAULT_PLACE_QUORUM,
    Activity,
    JoinVote,
    Membership,
    PlaceConfirmation,
    Post,
    Thread,
    UserPlaceProposal,
)


class SocialError(Exception):
    """Base for expected, user-facing social-domain errors."""


class NotEligible(SocialError):
    """User fails the participation/cohort gate for this action."""


class NotAMember(SocialError):
    """Action requires current membership the user doesn't have."""


class InvalidState(SocialError):
    """Target object is not in a state that permits this action."""


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
    guardian_accompanied=False,
):
    if not can_create_activity(owner):
        raise NotEligible(
            _("User cannot create activities (needs verification/consent + a cohort).")
        )
    if guardian_accompanied and owner.cohort != Cohort.CHILD:
        raise InvalidState(_("Only children's activities can be guardian-accompanied."))
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
        guardian_accompanied=guardian_accompanied,
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
    membership.save(update_fields=["state", "updated_at"])
    return membership


# Fields an owner may change on an OPEN, not-yet-started activity. Deliberately excludes
# place / activity_type / cohort / owner / guardian_accompanied: those define the meetup's
# identity and the cohort-isolation boundary, so an edit must never touch them (no
# bait-and-switch, no escaping the safety pin). See docs/SAFETY.md.
ACTIVITY_EDITABLE_FIELDS = ("title", "description", "starts_at", "ends_at", "capacity")


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

    time_changed = "starts_at" in fields and fields["starts_at"] != activity.starts_at
    if not fields:
        return activity
    for key, value in fields.items():
        setattr(activity, key, value)
    activity.save(update_fields=[*fields.keys(), "updated_at"])

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


def _admit(membership: Membership) -> None:
    membership.state = Membership.State.MEMBER
    membership.decided_at = timezone.now()
    membership.save(update_fields=["state", "decided_at", "updated_at"])
    _notify(
        membership.user,
        "join_approved",
        "You're in!",
        body=f"You were admitted to “{membership.activity.title}”.",
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
def post_to_thread(author, activity, body: str) -> Post:
    membership = current_members(activity).filter(user=author).first()
    if membership is None:
        raise NotAMember("Only current members can post in the activity thread.")
    if membership.role == Membership.Role.GUARDIAN:
        # Guardians accompany children's activities as transparent, read-only supervisors;
        # an adult must not post into a children's thread (cohort isolation for the peers).
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    if not can_participate(author):
        # Catches a member whose parental consent was revoked or assurance lapsed after join.
        raise NotEligible(_("Posting requires verified, consented participation."))
    return Post.objects.create(thread=activity.thread, author=author, body=body)


@transaction.atomic
def post_announcement(owner, activity, body: str) -> Post:
    """Owner-only pinned broadcast: a must-read logistics post that surfaces above the
    thread and fires one notification to every current member. Same cohort/consent gate
    as an ordinary post; only the owner may use it."""
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the organiser can post an announcement."))
    if not can_participate(owner):
        raise NotEligible(_("Posting requires verified, consented participation."))
    post = Post.objects.create(
        thread=activity.thread, author=owner, body=body, is_announcement=True
    )
    body_preview = body.strip()
    if len(body_preview) > 140:
        body_preview = body_preview[:139].rstrip() + "…"
    for membership in current_members(activity).exclude(user_id=owner.id).select_related("user"):
        _notify(
            membership.user,
            "announcement",
            _("Announcement: %(title)s") % {"title": activity.title},
            body=body_preview,
            url=f"/api/social/activities/{activity.id}/",
        )
    return post


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
