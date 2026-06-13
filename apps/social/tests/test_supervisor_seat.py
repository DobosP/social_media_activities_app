"""F29 — verified-adult supervisor seat for children's activities.

A supervised CHILD activity cannot SETTLE a join until the owner's OWN verified guardian is
seated as a read-only GUARDIAN supervisor. Presence is derived LIVE; the seat is keyed strictly
on is_guardian_of(guardian, OWNER) — never loosened to "any participant".
"""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import (
    InvalidState,
    NotAMember,
    active_supervisor_present,
    add_guardian,
    cast_vote,
    create_activity,
    leave_activity,
    owner_admit,
    request_to_join,
    set_activity_supervision,
    supervision_satisfied,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type(slug):
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    return ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)


def _supervised_activity(owner, slug):
    place = Place.objects.create(name=f"P-{slug}", location=PT, source=Place.Source.OSM)
    return create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Kids meetup",
        starts_at="2030-06-01T10:00Z",
        supervised=True,
    )


def test_supervised_implies_guardian_accompanied():
    child = _child("s1")
    activity = _supervised_activity(child, "s1")
    assert activity.supervised is True
    assert activity.guardian_accompanied is True  # auto-implied so a supervisor can be seated


def test_supervised_rejected_for_adult():
    adult = _adult("s2")
    place = Place.objects.create(name="P2", location=PT, source=Place.Source.OSM)
    with pytest.raises(InvalidState):
        create_activity(
            adult,
            place=place,
            activity_type=_type("s2"),
            title="x",
            starts_at="2030-06-01T10:00Z",
            supervised=True,
        )


def test_join_cannot_settle_without_supervisor():
    owner = _child("s3o")
    activity = _supervised_activity(owner, "s3")
    joiner = _child("s3j")
    request_to_join(joiner, activity)
    membership = Membership.objects.get(activity=activity, user=joiner)
    # Owner is the only voting member; their approval clears the threshold...
    cast_vote(owner, membership, True)
    membership.refresh_from_db()
    assert membership.state == Membership.State.REQUESTED  # ...but it can't settle: no supervisor


def test_owner_admit_blocked_with_clear_message():
    owner = _child("s4o")
    activity = _supervised_activity(owner, "s4")
    joiner = _child("s4j")
    request_to_join(joiner, activity)
    membership = Membership.objects.get(activity=activity, user=joiner)
    with pytest.raises(InvalidState):
        owner_admit(owner, membership)


def test_adding_supervisor_settles_pending_join():
    owner = _child("s5o")
    activity = _supervised_activity(owner, "s5")
    guardian = _adult("s5g")
    link_guardian(guardian, owner)
    joiner = _child("s5j")
    request_to_join(joiner, activity)
    membership = Membership.objects.get(activity=activity, user=joiner)
    cast_vote(owner, membership, True)  # cleared the vote, but stuck REQUESTED
    membership.refresh_from_db()
    assert membership.state == Membership.State.REQUESTED
    add_guardian(owner, activity, guardian)  # seating the supervisor unblocks the settle
    membership.refresh_from_db()
    assert membership.state == Membership.State.MEMBER


def test_supervisor_must_be_guardian_of_owner_not_any_participant():
    owner = _child("s6o")
    activity = _supervised_activity(owner, "s6")
    # An adult who is a guardian of a DIFFERENT child — seated manually — must NOT satisfy
    # supervision (the gate is keyed on is_guardian_of(guardian, OWNER), never loosened).
    other_child = _child("s6other")
    stranger_guardian = _adult("s6g")
    link_guardian(stranger_guardian, other_child)
    Membership.objects.create(
        activity=activity,
        user=stranger_guardian,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
    )
    assert active_supervisor_present(activity) is False
    assert supervision_satisfied(activity) is False


def test_live_presence_flips_when_supervisor_leaves():
    owner = _child("s7o")
    activity = _supervised_activity(owner, "s7")
    guardian = _adult("s7g")
    link_guardian(guardian, owner)
    add_guardian(owner, activity, guardian)
    assert active_supervisor_present(activity) is True
    leave_activity(guardian, activity)
    assert active_supervisor_present(activity) is False  # derived live, never a stale stored flag
    # A new join now cannot settle until a supervisor returns.
    joiner = _child("s7j")
    request_to_join(joiner, activity)
    m = Membership.objects.get(activity=activity, user=joiner)
    cast_vote(owner, m, True)
    m.refresh_from_db()
    assert m.state == Membership.State.REQUESTED


def test_toggle_supervision_on_and_off():
    owner = _child("s8")
    place = Place.objects.create(name="P8", location=PT, source=Place.Source.OSM)
    activity = create_activity(
        owner,
        place=place,
        activity_type=_type("s8"),
        title="x",
        starts_at="2030-06-01T10:00Z",
        guardian_accompanied=True,
    )
    assert activity.supervised is False
    set_activity_supervision(owner, activity, True)
    activity.refresh_from_db()
    assert activity.supervised is True and activity.guardian_accompanied is True
    set_activity_supervision(owner, activity, False)
    activity.refresh_from_db()
    assert activity.supervised is False


def test_toggle_off_releases_stuck_join():
    owner = _child("s9o")
    activity = _supervised_activity(owner, "s9")
    joiner = _child("s9j")
    request_to_join(joiner, activity)
    m = Membership.objects.get(activity=activity, user=joiner)
    cast_vote(owner, m, True)
    m.refresh_from_db()
    assert m.state == Membership.State.REQUESTED  # stuck (no supervisor)
    set_activity_supervision(owner, activity, False)  # no longer required
    m.refresh_from_db()
    assert m.state == Membership.State.MEMBER  # released


def test_non_owner_cannot_set_supervision():
    owner = _child("s10o")
    activity = _supervised_activity(owner, "s10")
    other = _child("s10x")
    with pytest.raises(NotAMember):
        set_activity_supervision(other, activity, False)
