"""Phase 4: self-only progression derived from confirmed real meetups (F22). Stores nothing,
shows nobody else's number, and others see the unchanged base avatar by default.
"""

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.recommendations.services import interest_avatar_svg, set_interests
from apps.social import services as social
from apps.social.models import Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type(slug="prog_sport"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    at, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": slug.title(), "category": cat}
    )
    return at


def _place():
    return Place.objects.create(
        name="Court",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
    )


def _activity_owned_by(user):
    return social.create_activity(
        user,
        place=_place(),
        activity_type=_type(),
        title="Meetup",
        starts_at=timezone.now() + timezone.timedelta(days=1),
    )


def _confirm(user, activity):
    # The owner is auto-seated MEMBER by create_activity; set the F22 "we met" signal directly.
    Membership.objects.filter(activity=activity, user=user).update(met_confirmed_at=timezone.now())


# --- the count: self-only, regresses ---


def test_count_is_self_only_and_levels_up():
    a, b = _user("a"), _user("b")
    act_a = _activity_owned_by(a)
    _activity_owned_by(b)  # b owns an activity but has not confirmed
    assert social.self_confirmed_meetup_count(a) == 0
    _confirm(a, act_a)
    assert social.self_confirmed_meetup_count(a) == 1
    assert social.self_confirmed_meetup_count(b) == 0  # never another user's
    assert social.progression_level(1) == 1
    assert social.progression_summary(a)["level"] == 1


def test_count_regresses_when_the_confirmation_clears():
    a = _user("a2")
    act = _activity_owned_by(a)
    _confirm(a, act)
    assert social.self_confirmed_meetup_count(a) == 1
    # Leaving an activity clears met_confirmed_at (F22) — current standing, not a high-score.
    Membership.objects.filter(activity=act, user=a).update(met_confirmed_at=None)
    assert social.self_confirmed_meetup_count(a) == 0


def test_progression_level_banding():
    assert social.progression_level(0) == 0
    assert social.progression_level(2) == 1
    assert social.progression_level(24) == len(social.PROGRESSION_THRESHOLDS)


# --- no observable signal to others (the inv.2 guarantee) ---


def test_others_see_base_avatar_regardless_of_progress(settings):
    settings.PROGRESSION_AVATAR_PUBLIC = False
    b = _user("b2")
    set_interests(b, [_type().slug])  # give B a constellation (intensity only affects that)
    before = interest_avatar_svg(b)
    act = _activity_owned_by(b)
    _confirm(b, act)
    after = interest_avatar_svg(b)
    # What other people see (the shared avatar) is byte-identical despite B's confirmed meetup.
    assert after == before


def test_public_flag_lets_progress_show_to_others(settings):
    b = _user("b3")
    set_interests(b, [_type().slug])
    settings.PROGRESSION_AVATAR_PUBLIC = True
    base = interest_avatar_svg(b)
    act = _activity_owned_by(b)
    _confirm(b, act)
    assert interest_avatar_svg(b) != base


def test_public_discovery_card_has_no_progression_field():
    from apps.discovery.serializers import ActivityCardSerializer

    a = _user("a3")
    act = _activity_owned_by(a)
    _confirm(a, act)
    data = ActivityCardSerializer(act).data
    assert "progression" not in data and "level" not in data and "count" not in data
