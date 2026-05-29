"""Domain logic for the social core: cohort-gated activities, join-by-vote, and the
user-place quorum. Views and admin go through these functions so the safety
invariants (cohort isolation, verified-and-consented participation) live in one place.
"""

from django.db import transaction
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

    qs = Activity.objects.filter(cohort=user.cohort)
    blocked = blocked_user_ids(user)
    if blocked:
        qs = qs.exclude(owner_id__in=blocked)
    return qs


def can_see_activity(user, activity) -> bool:
    return _has_cohort(user) and user.cohort == activity.cohort


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
    if not current_members(activity).filter(user=author).exists():
        raise NotAMember("Only current members can post in the activity thread.")
    return Post.objects.create(thread=activity.thread, author=author, body=body)


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
