"""W2-F27: read-aloud plain-language meetup brief.

Pure read-time composer; the KEY invariant is that visibility mirrors the fields it draws from —
cohort-visible chips show to anyone, member-only logistics only to a member — and it emits NO
numeric counts (so it can never leak a roster size to a minor).
"""

from datetime import timedelta

import pytest

from apps.social.models import Activity
from apps.social.services import create_activity, plain_meetup_brief

pytestmark = pytest.mark.django_db


def _activity(adult, place, activity_type, now):
    return create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Saturday Run",
        starts_at=now + timedelta(hours=2),
        cost_band=Activity.CostBand.FREE,
        difficulty=Activity.Difficulty.EASY,
        accessibility_notes="Step-free entrance.",
        meeting_point="By the north gate",
        what_to_bring="Water",
        organizer_note="We start on time",
        getting_home_note="Bus 25 outside",
        first_time_note="Look for the blue flag",
    )


def _labels(brief):
    return [label for label, _ in brief]


def _text(brief):
    return " ".join(f"{label} {sentence}" for label, sentence in brief)


def test_brief_always_includes_what_type_place_time(adult, place, activity_type, now):
    brief = plain_meetup_brief(_activity(adult, place, activity_type, now), is_member=False)
    text = _text(brief)
    assert "Saturday Run" in text  # title
    assert "Basketball" in text  # activity_type.name
    assert "Community Hall" in text  # place.name
    assert {"What", "Activity", "Where", "When"} <= set(_labels(brief))


def test_cohort_visible_chips_shown_even_to_non_member(adult, place, activity_type, now):
    text = _text(plain_meetup_brief(_activity(adult, place, activity_type, now), is_member=False))
    assert "Free" in text  # cost_band display
    assert "Easy" in text  # difficulty display
    assert "Step-free entrance." in text  # accessibility_notes


def test_member_only_logistics_hidden_from_non_member(adult, place, activity_type, now):
    a = _activity(adult, place, activity_type, now)
    non_member = _text(plain_meetup_brief(a, is_member=False))
    for secret in ("north gate", "Water", "start on time", "Bus 25", "blue flag"):
        assert secret not in non_member
    # ...and the same logistics ARE shown to a member.
    member = _text(plain_meetup_brief(a, is_member=True))
    for shown in ("north gate", "Water", "Bus 25", "blue flag"):
        assert shown in member


def test_brief_emits_no_numeric_counts(adult, place, activity_type, now):
    # It must never carry a going/roster count (sidesteps thread_digest's count-suppression).
    text = _text(plain_meetup_brief(_activity(adult, place, activity_type, now), is_member=True))
    assert "going" not in text.lower()
    assert "member" not in text.lower()


def test_empty_optional_fields_are_omitted(adult, place, activity_type, now):
    bare = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Bare",
        starts_at=now + timedelta(hours=2),
    )
    labels = _labels(plain_meetup_brief(bare, is_member=True))
    assert "Cost" not in labels and "Where to meet" not in labels  # unset -> omitted
    assert {"What", "Activity", "Where", "When"} <= set(labels)  # core four always present
