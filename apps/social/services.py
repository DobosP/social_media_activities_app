"""Domain logic for the social core: cohort-gated activities, join-by-vote, and the
user-place quorum. Views and admin go through these functions so the safety
invariants (cohort isolation, verified-and-consented participation) live in one place.
"""

from django.db import transaction
from django.utils import timezone

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
    """Activities a user may see — only those in their own cohort (isolation)."""
    if not _has_cohort(user):
        return Activity.objects.none()
    return Activity.objects.filter(cohort=user.cohort)


def can_see_activity(user, activity) -> bool:
    return _has_cohort(user) and user.cohort == activity.cohort


def current_members(activity):
    return activity.memberships.filter(state=Membership.State.MEMBER)


def can_join(user, activity) -> bool:
    if not can_participate(user):
        return False
    if user.cohort != activity.cohort:
        return False
    if activity.status != Activity.Status.OPEN:
        return False
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
):
    if not can_create_activity(owner):
        raise NotEligible("User cannot create activities (needs verification/consent + a cohort).")
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
        raise NotEligible("User is not eligible to join this activity.")
    return Membership.objects.create(
        activity=activity,
        user=user,
        role=Membership.Role.MEMBER,
        state=Membership.State.REQUESTED,
    )


def _admit(membership: Membership) -> None:
    membership.state = Membership.State.MEMBER
    membership.decided_at = timezone.now()
    membership.save(update_fields=["state", "decided_at", "updated_at"])


def _evaluate_vote(membership: Membership) -> None:
    """Promote a requested membership to member once approvals clear the threshold."""
    member_count = current_members(membership.activity).count()
    if member_count == 0:
        return
    approvals = membership.votes.filter(approve=True).count()
    if approvals / member_count >= membership.activity.join_threshold:
        _admit(membership)


@transaction.atomic
def cast_vote(voter, membership: Membership, approve: bool) -> Membership:
    activity = membership.activity
    if membership.state != Membership.State.REQUESTED:
        raise InvalidState("This membership is not awaiting a join vote.")
    if membership.user_id == voter.id:
        raise InvalidState("A requester cannot vote on their own join request.")
    if not current_members(activity).filter(user=voter).exists():
        raise NotAMember("Only current members may vote on join requests.")
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
        raise NotAMember("Only the activity owner may override.")
    if not activity.owner_can_override:
        raise InvalidState("Owner override is disabled for this activity.")
    if membership.state != Membership.State.REQUESTED:
        raise InvalidState("This membership is not awaiting a join vote.")
    _admit(membership)
    return membership


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
