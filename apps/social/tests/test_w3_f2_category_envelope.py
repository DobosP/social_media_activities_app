"""W3-F2: the guardian activity-category allowlist, enforced at ALL FOUR child chokepoints.

The load-bearing requirement: a CHILD organizer is auto-seated MEMBER inside create_activity
WITHOUT passing the join gate, so enforcing only at can_join would be a child-safety ILLUSION.
These pin the envelope at can_join, create_activity, create_series AND propose_interest, plus the
ancestry walk (a type under a SUB-category of an allowed top category passes) and the fail-closed
empty intersection across guardians.
"""

from datetime import timedelta

import pytest

from apps.accounts.models import AgeBand
from apps.accounts.services import link_guardian, set_guardian_guardrail
from apps.social.models import ActivityInterest, ActivitySeries
from apps.social.services import (
    InvalidState,
    can_join,
    category_envelope_allows,
    create_activity,
    create_series,
    propose_interest,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

from .conftest import make_user

pytestmark = pytest.mark.django_db

_CADENCE = ActivitySeries.Cadence.values[0]
_WINDOW = ActivityInterest.CoarseWindow.values[0]


@pytest.fixture
def cats(db):
    """A small taxonomy: sport > ball-sports (with basketball), and a separate reading category."""
    sport = ActivityCategory.objects.create(slug="f2-sport", name="Sport")
    ball = ActivityCategory.objects.create(slug="f2-ball", name="Ball sports", parent=sport)
    reading = ActivityCategory.objects.create(slug="f2-reading", name="Reading")
    basketball = ActivityType.objects.create(
        slug="f2-basketball", name="Basketball", category=ball, is_active=True
    )
    bookclub = ActivityType.objects.create(
        slug="f2-bookclub", name="Book club", category=reading, is_active=True
    )
    return {"basketball": basketball, "bookclub": bookclub}


def _sport_only_child(name, guardian_name):
    """A CHILD whose single guardian allows ONLY the sport category."""
    child = make_user(name, AgeBand.UNDER_16, consented=True)
    guardian = make_user(guardian_name)  # ADULT
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, allowed_categories=["f2-sport"])
    return child


def test_envelope_helper_ancestry_and_no_restriction(cats):
    child = _sport_only_child("f2c_helper", "f2g_helper")
    # basketball is under ball-sports under sport -> allowed by ANCESTRY (not a direct match).
    assert category_envelope_allows(child, cats["basketball"]) is True
    assert category_envelope_allows(child, cats["bookclub"]) is False
    # An adult is never constrained by a category envelope (and pays no query).
    assert category_envelope_allows(make_user("f2adult"), cats["bookclub"]) is True


def test_can_join_blocks_disallowed_category(cats, place, now):
    child = _sport_only_child("f2c_join", "f2g_join")
    owner = make_user("f2owner_join", AgeBand.UNDER_16, consented=True)  # no guardrail
    reading_act = create_activity(
        owner,
        place=place,
        activity_type=cats["bookclub"],
        title="Books",
        starts_at=now + timedelta(hours=2),
    )
    sport_act = create_activity(
        owner,
        place=place,
        activity_type=cats["basketball"],
        title="Hoops",
        starts_at=now + timedelta(hours=2),
    )
    assert can_join(child, reading_act) is False  # outside the envelope
    assert can_join(child, sport_act) is True  # inside it


def test_create_activity_blocks_child_organizing_disallowed_category(cats, place, now):
    # THE illusion-closer: the CHILD organizer is auto-seated MEMBER without passing can_join,
    # so the envelope MUST bite at create time too — else a child escapes it by organizing.
    child = _sport_only_child("f2c_create", "f2g_create")
    with pytest.raises(InvalidState):
        create_activity(
            child,
            place=place,
            activity_type=cats["bookclub"],
            title="Books",
            starts_at=now + timedelta(hours=2),
        )
    allowed = create_activity(
        child,
        place=place,
        activity_type=cats["basketball"],
        title="Hoops",
        starts_at=now + timedelta(hours=2),
    )
    assert allowed.pk is not None


def test_create_series_blocks_disallowed_category(cats, place, now):
    child = _sport_only_child("f2c_series", "f2g_series")
    with pytest.raises(InvalidState):
        create_series(
            child,
            place=place,
            activity_type=cats["bookclub"],
            title="Books",
            cadence=_CADENCE,
            first_starts_at=now + timedelta(days=1),
        )
    series = create_series(
        child,
        place=place,
        activity_type=cats["basketball"],
        title="Hoops",
        cadence=_CADENCE,
        first_starts_at=now + timedelta(days=1),
    )
    assert series.pk is not None


def test_propose_interest_blocks_disallowed_category(cats, place, now):
    child = _sport_only_child("f2c_gauge", "f2g_gauge")
    with pytest.raises(InvalidState):
        propose_interest(child, place=place, activity_type=cats["bookclub"], coarse_window=_WINDOW)
    gauge = propose_interest(
        child, place=place, activity_type=cats["basketball"], coarse_window=_WINDOW
    )
    assert gauge.pk is not None


def test_no_envelope_allows_everything(cats, place, now):
    child = make_user("f2c_free", AgeBand.UNDER_16, consented=True)
    link_guardian(make_user("f2g_free"), child)  # linked, but NO category guardrail
    act = create_activity(
        child,
        place=place,
        activity_type=cats["bookclub"],
        title="Books",
        starts_at=now + timedelta(hours=2),
    )
    assert act.pk is not None


def test_empty_intersection_blocks_all_categories(cats, place, now):
    # Two guardians with disjoint allowlists -> empty intersection -> NOTHING passes (fail-closed),
    # blocking even the category each guardian individually allowed.
    child = make_user("f2c_empty", AgeBand.UNDER_16, consented=True)
    g1, g2 = make_user("f2g_e1"), make_user("f2g_e2")
    link_guardian(g1, child)
    link_guardian(g2, child)
    set_guardian_guardrail(g1, child, allowed_categories=["f2-sport"])
    set_guardian_guardrail(g2, child, allowed_categories=["f2-reading"])
    assert category_envelope_allows(child, cats["basketball"]) is False
    with pytest.raises(InvalidState):
        create_activity(
            child,
            place=place,
            activity_type=cats["basketball"],
            title="Hoops",
            starts_at=now + timedelta(hours=2),
        )
