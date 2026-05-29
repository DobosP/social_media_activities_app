from django.conf import settings
from django.db import models
from django.db.models import Q, UniqueConstraint

from apps.accounts.models import Cohort

# Default join-by-vote threshold: a join request passes when at least this fraction
# of current members approve. Two-thirds per the product spec (configurable per activity).
DEFAULT_JOIN_THRESHOLD = 2 / 3

# Independent confirmations required before a user-submitted place is published.
DEFAULT_PLACE_QUORUM = 3


class Activity(models.Model):
    """A meetup: a Place (D1) + ActivityType (D1) + a time window + an owner.

    Cohort-scoped for safety: the activity is pinned to its owner's cohort at
    creation, and visibility/joining are restricted to that same cohort so children
    only meet similar-age peers (see docs/SAFETY.md).
    """

    class Status(models.TextChoices):
        OPEN = "open", "Open"
        CANCELLED = "cancelled", "Cancelled"
        COMPLETED = "completed", "Completed"

    owner = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="owned_activities"
    )
    place = models.ForeignKey(
        "places.Place", on_delete=models.PROTECT, related_name="social_activities"
    )
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType", on_delete=models.PROTECT, related_name="social_activities"
    )

    title = models.CharField(max_length=200)
    description = models.TextField(blank=True)

    starts_at = models.DateTimeField()
    ends_at = models.DateTimeField(null=True, blank=True)

    # Pinned from the owner's cohort at creation; the isolation boundary.
    cohort = models.CharField(max_length=16, choices=Cohort.choices)

    join_threshold = models.FloatField(default=DEFAULT_JOIN_THRESHOLD)
    owner_can_override = models.BooleanField(default=True)
    capacity = models.PositiveIntegerField(null=True, blank=True)
    # Children's activities may allow a parent/guardian to accompany (supervised,
    # group-only). Only meaningful for the CHILD cohort. See docs/SAFETY.md.
    guardian_accompanied = models.BooleanField(default=False)

    status = models.CharField(max_length=16, choices=Status.choices, default=Status.OPEN)
    # Set by a moderator REMOVE action; hidden content is excluded from every member-facing
    # query (discovery, recommendations) but retained for audit/appeal. See apps/safety.
    is_hidden = models.BooleanField(default=False)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        verbose_name_plural = "activities"
        constraints = [
            models.CheckConstraint(
                condition=Q(join_threshold__gt=0) & Q(join_threshold__lte=1),
                name="activity_threshold_fraction",
            ),
        ]
        indexes = [
            models.Index(fields=["cohort", "status"]),
            models.Index(fields=["starts_at"]),
        ]

    def __str__(self):
        return self.title


class Membership(models.Model):
    """A user's relationship to an activity, with a role and a lifecycle state.

    A `requested` membership is the pending join request that members vote on.
    """

    class Role(models.TextChoices):
        OWNER = "owner", "Owner"
        MEMBER = "member", "Member"
        GUARDIAN = "guardian", "Guardian"  # accompanying parent/guardian (supervisory)

    class State(models.TextChoices):
        REQUESTED = "requested", "Requested"
        MEMBER = "member", "Member"
        REMOVED = "removed", "Removed"

    activity = models.ForeignKey(Activity, on_delete=models.CASCADE, related_name="memberships")
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="memberships"
    )
    role = models.CharField(max_length=16, choices=Role.choices, default=Role.MEMBER)
    state = models.CharField(max_length=16, choices=State.choices, default=State.REQUESTED)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)
    decided_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["activity", "user"], name="uq_membership_activity_user"),
        ]
        indexes = [models.Index(fields=["activity", "state"])]

    def __str__(self):
        return f"{self.user} @ {self.activity} ({self.state})"


class JoinVote(models.Model):
    """A current member's vote on a pending join request (the requested Membership)."""

    membership = models.ForeignKey(Membership, on_delete=models.CASCADE, related_name="votes")
    voter = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="join_votes"
    )
    approve = models.BooleanField()
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["membership", "voter"], name="uq_joinvote_membership_voter"),
        ]

    def __str__(self):
        return f"{self.voter} {'+' if self.approve else '-'} {self.membership_id}"


class Thread(models.Model):
    """The text-only discussion thread for an activity (one per activity)."""

    activity = models.OneToOneField(Activity, on_delete=models.CASCADE, related_name="thread")
    created_at = models.DateTimeField(auto_now_add=True)

    def __str__(self):
        return f"thread({self.activity})"


class Post(models.Model):
    """A text post in an activity thread. Text-first: no media here (photos are D6)."""

    thread = models.ForeignKey(Thread, on_delete=models.CASCADE, related_name="posts")
    author = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="posts"
    )
    body = models.TextField()
    # Set by a moderator REMOVE action; hidden posts are excluded from thread reads but
    # retained for audit/appeal.
    is_hidden = models.BooleanField(default=False)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        ordering = ["created_at"]
        indexes = [models.Index(fields=["thread", "created_at"])]

    def __str__(self):
        return f"post({self.author} @ {self.thread_id})"


class UserPlaceProposal(models.Model):
    """A user-submitted place awaiting a multi-user quorum before it goes public.

    Co-creation: N independent users (not the proposer) must confirm before the
    place is published, plugging into D1's `Place.source="user"` seam.
    """

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        PUBLISHED = "published", "Published"
        REJECTED = "rejected", "Rejected"

    place = models.OneToOneField("places.Place", on_delete=models.CASCADE, related_name="proposal")
    proposer = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="place_proposals"
    )
    required_confirmations = models.PositiveIntegerField(default=DEFAULT_PLACE_QUORUM)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    published_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            models.CheckConstraint(
                condition=Q(required_confirmations__gte=1),
                name="proposal_quorum_positive",
            ),
        ]

    def __str__(self):
        return f"proposal({self.place_id}, {self.status})"


class PlaceConfirmation(models.Model):
    """An independent user's confirmation of a proposed place."""

    proposal = models.ForeignKey(
        UserPlaceProposal, on_delete=models.CASCADE, related_name="confirmations"
    )
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="place_confirmations"
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["proposal", "user"], name="uq_confirmation_proposal_user"),
        ]

    def __str__(self):
        return f"confirm({self.proposal_id} by {self.user})"
