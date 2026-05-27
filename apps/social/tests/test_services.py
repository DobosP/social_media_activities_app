import pytest

from apps.accounts.models import AgeBand, User
from apps.social.models import Activity, Membership, UserPlaceProposal
from apps.social.services import (
    InvalidState,
    NotAMember,
    NotEligible,
    can_join,
    cast_vote,
    confirm_place,
    create_activity,
    owner_admit,
    post_to_thread,
    propose_place,
    request_to_join,
    visible_activities,
)

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Pickup game",
        starts_at=now,
        **kw,
    )


def test_create_activity_pins_cohort_and_adds_owner(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    assert activity.cohort == adult.cohort
    owner_m = activity.memberships.get(user=adult)
    assert owner_m.role == Membership.Role.OWNER
    assert owner_m.state == Membership.State.MEMBER
    assert hasattr(activity, "thread")


def test_unverified_user_cannot_create(place, activity_type, now):
    unverified = User.objects.create_user(username="u", password="pw", age_band=AgeBand.ADULT)
    with pytest.raises(NotEligible):
        _activity(unverified, place, activity_type, now)


def test_minor_without_consent_cannot_create(place, activity_type, now):
    minor = make_user("nochild", AgeBand.UNDER_16, consented=False)
    with pytest.raises(NotEligible):
        _activity(minor, place, activity_type, now)


def test_cohort_isolation_on_visibility_and_join(adult, child, place, activity_type, now):
    adult_activity = _activity(adult, place, activity_type, now)

    # The child (different cohort) cannot see or join the adult's activity.
    assert adult_activity not in visible_activities(child)
    assert can_join(child, adult_activity) is False
    with pytest.raises(NotEligible):
        request_to_join(child, adult_activity)

    # Same-cohort peer can.
    peer = make_user("adultpeer", AgeBand.ADULT)
    assert adult_activity in visible_activities(peer)
    assert can_join(peer, adult_activity) is True


def test_join_by_vote_two_thirds(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    # Add a second confirmed member so there are 2 current members.
    m2 = request_to_join(adult2, activity)
    owner_admit(adult, m2)
    assert m2.state == Membership.State.MEMBER

    requester = make_user("joiner", AgeBand.ADULT)
    request = request_to_join(requester, activity)

    # 2 members, threshold 2/3 → need both to approve (1/2 = 0.5 < 0.667).
    cast_vote(adult, request, True)
    request.refresh_from_db()
    assert request.state == Membership.State.REQUESTED

    cast_vote(adult2, request, True)
    request.refresh_from_db()
    assert request.state == Membership.State.MEMBER


def test_requester_cannot_vote_and_nonmember_cannot_vote(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    requester = make_user("joiner2", AgeBand.ADULT)
    request = request_to_join(requester, activity)

    with pytest.raises(InvalidState):
        cast_vote(requester, request, True)

    outsider = make_user("outsider", AgeBand.ADULT)
    with pytest.raises(NotAMember):
        cast_vote(outsider, request, True)


def test_owner_override_admits_directly(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    requester = make_user("joiner3", AgeBand.ADULT)
    request = request_to_join(requester, activity)
    owner_admit(adult, request)
    request.refresh_from_db()
    assert request.state == Membership.State.MEMBER


def test_only_members_can_post(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    post = post_to_thread(adult, activity, "Who's in this weekend?")
    assert post.body == "Who's in this weekend?"

    outsider = make_user("lurker", AgeBand.ADULT)
    with pytest.raises(NotAMember):
        post_to_thread(outsider, activity, "let me in")


def test_place_quorum_publishes_after_independent_confirmations(adult, place):
    proposal = propose_place(adult, place, required_confirmations=2)
    assert proposal.status == UserPlaceProposal.Status.PENDING

    # Proposer cannot self-confirm.
    with pytest.raises(InvalidState):
        confirm_place(adult, proposal)

    confirm_place(make_user("c1", AgeBand.ADULT), proposal)
    proposal.refresh_from_db()
    assert proposal.status == UserPlaceProposal.Status.PENDING

    confirm_place(make_user("c2", AgeBand.ADULT), proposal)
    proposal.refresh_from_db()
    assert proposal.status == UserPlaceProposal.Status.PUBLISHED
    assert proposal.published_at is not None


def test_cannot_join_twice(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    request_to_join(adult2, activity)
    assert can_join(adult2, activity) is False
    with pytest.raises(NotEligible):
        request_to_join(adult2, activity)


def test_default_threshold_is_two_thirds(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    assert activity.join_threshold == pytest.approx(2 / 3)
    assert Activity.objects.count() == 1
