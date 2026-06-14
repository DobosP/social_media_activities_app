"""W2-F1: taxonomy-aware, typo-tolerant activity search.

Search resolves the activity TYPE through its slug + RO/EN aliases + a depth-1 synonym/variant
walk (so seeded vocabulary actually matches), with an honest trigram "did you mean" on zero
results. All of it rides visible_activities, so cohort isolation + blocking are untouched.
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.models import AgeBand
from apps.places.models import Place
from apps.social import services as social
from apps.taxonomy.models import ActivityCategory, ActivityRelation, ActivityType

from .conftest import make_user

pytestmark = pytest.mark.django_db


@pytest.fixture
def cat(db):
    c, _ = ActivityCategory.objects.get_or_create(slug="w2f1-sport", defaults={"name": "Sport"})
    return c


def _type(cat, slug, name, aliases=None):
    return ActivityType.objects.create(slug=slug, name=name, category=cat, aliases=aliases or [])


def _place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _activity(owner, atype, *, title="Game"):
    return social.create_activity(
        owner,
        place=_place(),
        activity_type=atype,
        title=title,
        starts_at=timezone.now() + timedelta(days=1),
    )


def _titles(qs):
    return {a.title for a in qs}


def test_alias_match_finds_activity(cat):
    owner = make_user("w2f1_o1")
    fb = _type(cat, "w2f1-football", "Football", aliases=["soccer", "fotbal"])
    _activity(owner, fb, title="Sunday football")
    # The display name is "Football" — searching an RO/EN alias must still find it.
    assert "Sunday football" in _titles(social.search_activities(owner, "fotbal"))
    assert "Sunday football" in _titles(social.search_activities(owner, "soccer"))


def test_slug_match_finds_activity(cat):
    owner = make_user("w2f1_o2")
    sb = _type(cat, "streetball", "Street Basketball", aliases=[])
    _activity(owner, sb, title="3v3 streetball")
    # Query matches the slug even though it is not a substring of the display name.
    assert "3v3 streetball" in _titles(social.search_activities(owner, "streetball"))


def test_depth1_synonym_walk(cat):
    owner = make_user("w2f1_o3")
    soccer = _type(cat, "w2f1-soccer", "Soccer", aliases=[])
    futsal = _type(cat, "w2f1-futsal", "Futsal", aliases=[])
    ActivityRelation.objects.create(
        source=soccer, target=futsal, kind=ActivityRelation.Kind.SYNONYM, symmetric=True
    )
    _activity(owner, futsal, title="Indoor futsal")
    # Searching "soccer" walks the synonym edge to futsal and finds the futsal activity.
    assert "Indoor futsal" in _titles(social.search_activities(owner, "soccer"))


def test_unrelated_query_finds_nothing(cat):
    owner = make_user("w2f1_o4")
    fb = _type(cat, "w2f1-fb2", "Football", aliases=["fotbal"])
    _activity(owner, fb, title="Football match")
    assert _titles(social.search_activities(owner, "knitting")) == set()


def test_search_respects_cohort_isolation(cat):
    # A child's activity must never surface in an adult's search (visible_activities wall).
    child = make_user("w2f1_child", AgeBand.UNDER_16, consented=True)
    adult = make_user("w2f1_adult")
    fb = _type(cat, "w2f1-fb3", "Football", aliases=["fotbal"])
    # child-owned activity (child cohort)
    social.create_activity(
        child,
        place=_place(),
        activity_type=fb,
        title="Kids football",
        starts_at=timezone.now() + timedelta(days=1),
    )
    assert "Kids football" not in _titles(social.search_activities(adult, "fotbal"))


def test_did_you_mean_suggests_close_actionable_type(cat):
    owner = make_user("w2f1_o5")
    # A fabricated, non-seeded name so the trigram match is unambiguous. "glarnbll" is NOT a
    # substring of "Glarnball" (so the literal search finds nothing) but is trigram-close.
    gb = _type(cat, "w2f1-glarn1", "Glarnball", aliases=[])
    _activity(owner, gb, title="Glarnball night")  # an actionable activity of this type exists
    assert social.search_activities(owner, "glarnbll").count() == 0  # no literal match
    assert social.search_did_you_mean(owner, "glarnbll") == "Glarnball"


def test_did_you_mean_none_when_no_actionable_match(cat):
    owner = make_user("w2f1_o6")
    _type(cat, "w2f1-glarn2", "Glarnball", aliases=[])  # type exists but NO upcoming activity
    # No dead-end suggestions: a close type with nothing to join yields no "did you mean".
    assert social.search_did_you_mean(owner, "glarnbll") is None
    # And a genuinely-unrelated query suggests nothing.
    assert social.search_did_you_mean(owner, "zzzzqqqq") is None


def test_fotbal_alias_seeded_on_football_type():
    # The load-bearing revise fix: the seed migration adds the RO 'fotbal' to football.
    fb = ActivityType.objects.filter(slug="football").first()
    if fb is None:
        pytest.skip("football type not seeded in this DB")
    assert "fotbal" in [a.lower() for a in (fb.aliases or [])]
