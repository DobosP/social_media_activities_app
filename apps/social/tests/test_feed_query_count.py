"""W2-2: the activities feed must not issue per-row COUNT queries (N+1).

``with_counts`` annotates member/participant counts so serializing the feed costs a
constant number of queries regardless of how many activities (or members) it contains.
See docs/PRODUCTION_HARDENING_PLAN_2026-05.md (PERF-1)."""

import pytest
from django.utils import timezone

from apps.social.models import Activity, Membership
from apps.social.serializers import ActivitySerializer
from apps.social.services import create_activity, visible_activities, with_counts

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, title, capacity=None):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title=title,
        starts_at=timezone.now(),
        capacity=capacity,
    )


def _add_member(activity, user):
    Membership.objects.create(
        activity=activity,
        user=user,
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )


def test_feed_serialization_is_constant_query_count(
    adult, place, activity_type, django_assert_num_queries
):
    # Three activities, each with a few members and one capped — the shape that would
    # trigger N+1 (member_count + open_positions) without annotation.
    for i in range(3):
        act = _activity(adult, place, activity_type, f"Game {i}", capacity=10 if i == 0 else None)
        for j in range(4):
            _add_member(act, make_user(f"m{i}_{j}"))

    qs = with_counts(
        visible_activities(adult).select_related("owner", "place", "activity_type", "thread")
    )

    # A single query materializes the annotated rows; serialization adds none.
    with django_assert_num_queries(1):
        data = ActivitySerializer(list(qs), many=True).data
    assert len(data) == 3
    # member_count is intentionally NOT serialized any more (the vanity-count removal); only the
    # functional open_positions remains, derived from the participant_n annotation (no N+1).
    by_title = {row["title"]: row for row in data}
    assert "member_count" not in by_title["Game 0"]
    assert by_title["Game 0"]["open_positions"] == 5  # capacity 10 - 5 participants
    assert by_title["Game 1"]["open_positions"] is None  # uncapped


def test_counts_exclude_guardian_from_positions(adult, child, place, activity_type):
    # A guardian-accompanied child activity: the guardian holds no position.
    act = create_activity(
        child,
        place=place,
        activity_type=activity_type,
        title="Kids game",
        starts_at=timezone.now(),
        capacity=4,
        guardian_accompanied=True,
    )
    Membership.objects.create(
        activity=act,
        user=adult,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )
    row = ActivitySerializer(with_counts(Activity.objects.filter(id=act.id)).first()).data
    assert "member_count" not in row  # member_count is no longer a serialized field
    assert row["open_positions"] == 3  # but only the owner holds a position (4 - 1)
