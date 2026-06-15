"""W2-F34: "who can see this" audience legibility for the thread composer.

Pure read-only re-description of gates that already hold. The load-bearing invariant: the peer count
is ADULT-viewer-only (a minor never sees a roster size). A supervisory guardian is cross-cohort and
CANNOT read the thread (can_read_thread walls out other cohorts), so the summary deliberately names
no guardian — it would be a false "an adult is reading" claim.
"""

from datetime import timedelta

import pytest
from django.contrib.auth.models import AnonymousUser

from apps.accounts.models import AgeBand
from apps.social.models import Membership
from apps.social.services import create_activity, thread_audience_summary

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=now + timedelta(hours=2),
        **kw,
    )


def _member(activity, user, role=Membership.Role.MEMBER):
    return activity.memberships.create(user=user, role=role, state=Membership.State.MEMBER)


def test_peer_count_for_adult_excludes_self(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)  # adult is owner-member
    _member(activity, adult2)
    summary = thread_audience_summary(adult, activity)
    assert summary["peer_count"] == 1  # adult2 only — never counts the viewer themselves
    assert summary["is_group"] is False


def test_peer_count_is_none_for_a_cross_cohort_adult(child, place, activity_type, now):
    # Self-protection: an adult viewer of a CHILD thread (the shape a supervisory guardian would
    # have) gets None — the helper never counts a thread the viewer can't legitimately read.
    activity = _activity(child, place, activity_type, now)  # CHILD cohort
    adult_viewer = make_user("adult_crosscohort")
    assert thread_audience_summary(adult_viewer, activity)["peer_count"] is None


@pytest.mark.parametrize("band", [AgeBand.UNDER_16, AgeBand.AGE_16_17])
def test_peer_count_suppressed_for_child_and_teen(band, place, activity_type, now):
    viewer = make_user(f"minor_{band}", band, consented=True)
    activity = _activity(viewer, place, activity_type, now)  # owned by a minor -> minor cohort
    assert (
        thread_audience_summary(viewer, activity)["peer_count"] is None
    )  # no roster size to a minor


def test_peer_count_suppressed_for_anonymous_viewer(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    assert thread_audience_summary(AnonymousUser(), activity)["peer_count"] is None


def test_group_thread_counts_peers(adult, place, activity_type, now):
    from apps.communities.models import Area
    from apps.social.services import create_group

    staff = make_user("f34_staff")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-f34", name="Cluj-Napoca")
    group = create_group(staff, area=area, title="Runners", activity_type=activity_type)
    group.memberships.create(user=adult, state="member")
    summary = thread_audience_summary(staff, group)
    assert summary["is_group"] is True
    assert summary["peer_count"] == 1  # adult (the staff owner-member is excluded as self)
