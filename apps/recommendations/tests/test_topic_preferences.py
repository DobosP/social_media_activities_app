"""Topic preferences — the user's (or a guardian's) STATED hand on the suggestion algorithm.

A SOFT signal over taxonomy categories: it re-orders + honestly labels cohort-visible
suggestions and NEVER hides anything or widens visibility past the cohort wall. Mirrors the
places.AccessPreference contract.
"""

import pytest
from django.contrib.auth.models import AnonymousUser
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.recommendations import services
from apps.recommendations.models import TopicPreference
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _types():
    sport = ActivityCategory.objects.create(slug="tp-sport", name="Sport")
    tabletop = ActivityCategory.objects.create(slug="tp-table", name="Tabletop")
    # A sub-category under sport, to prove the ancestry walk (picking the parent matches the child).
    team = ActivityCategory.objects.create(slug="tp-team", name="Team sport", parent=sport)
    bball = ActivityType.objects.create(slug="tp-bball", name="Basketball", category=team)
    chess = ActivityType.objects.create(slug="tp-chess", name="Chess", category=tabletop)
    return sport, tabletop, bball, chess


def _activity(owner, atype, title="A"):
    place = Place.objects.create(
        name="P", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title=title, starts_at="2030-01-01T10:00Z"
    )


# --- service: stated, idempotent, unknown-tolerant -----------------------------------------


def test_set_and_get_topic_preferences_replace_and_ignore_unknown():
    me = _user("tp-me")
    _types()
    services.set_topic_preferences(me, ["tp-sport", "does-not-exist"])
    assert services.topic_preference_slugs(me) == frozenset({"tp-sport"})
    assert {c.slug for c in services.get_topic_categories(me)} == {"tp-sport"}

    # Replace (not stack): the previous selection is fully overwritten.
    services.set_topic_preferences(me, ["tp-table"])
    assert services.topic_preference_slugs(me) == frozenset({"tp-table"})
    assert TopicPreference.objects.filter(user=me).count() == 1

    # Clearing.
    services.set_topic_preferences(me, [])
    assert services.topic_preference_slugs(me) == frozenset()


def test_topic_preference_slugs_anonymous_is_empty():
    assert services.topic_preference_slugs(AnonymousUser()) == frozenset()
    assert list(services.get_topic_categories(AnonymousUser())) == []


def test_activity_matches_topics_via_category_ancestry():
    owner = _user("tp-o")
    sport, tabletop, bball, chess = _types()
    a_bball = _activity(owner, bball, "Hoops")  # type -> team -> sport
    a_chess = _activity(owner, chess, "Chess")  # type -> tabletop

    # Picking the PARENT topic ("sport") matches a sub-category type (basketball).
    assert services.activity_matches_topics(a_bball, {"tp-sport"}) is True
    assert services.activity_matches_topics(a_chess, {"tp-sport"}) is False
    # Empty selection never matches.
    assert services.activity_matches_topics(a_bball, frozenset()) is False


# --- the SOFT nudge: floats matches, hides nothing -----------------------------------------


def test_sort_by_topic_match_floats_matches_and_hides_nothing():
    owner = _user("tp-o2")
    sport, tabletop, bball, chess = _types()
    a_bball = _activity(owner, bball, "Hoops")
    a_chess = _activity(owner, chess, "Chess")

    # chess first in input; with a sport topic, basketball floats up but chess STAYS in the list.
    ordered = services.sort_by_topic_match([a_chess, a_bball], {"tp-sport"})
    assert [a.id for a in ordered] == [a_bball.id, a_chess.id]
    assert a_chess.id in [a.id for a in ordered]  # nothing hidden

    # No-op when the viewer chose no topics (input order preserved, nothing dropped).
    same = services.sort_by_topic_match([a_chess, a_bball], frozenset())
    assert [a.id for a in same] == [a_chess.id, a_bball.id]


def test_sort_by_topic_match_is_stable_within_groups():
    owner = _user("tp-o3")
    sport, tabletop, bball, chess = _types()
    b1 = _activity(owner, bball, "Hoops 1")
    b2 = _activity(owner, bball, "Hoops 2")
    c1 = _activity(owner, chess, "Chess 1")
    # Two matches + one non-match: matches keep their relative order, non-match trails.
    ordered = services.sort_by_topic_match([b1, c1, b2], {"tp-sport"})
    assert [a.id for a in ordered] == [b1.id, b2.id, c1.id]


# --- feed integration: honest reason + nudge, cohort wall intact ---------------------------


def test_recommended_with_reasons_labels_and_floats_chosen_topic():
    owner = _user("tp-owner")
    me = _user("tp-feed")
    sport, tabletop, bball, chess = _types()
    a_bball = _activity(owner, bball, "Basketball")
    _activity(owner, chess, "Chess night")

    services.set_topic_preferences(me, ["tp-sport"])
    feed = services.recommended_with_reasons(me, limit=10)
    assert feed, "expected recommendations"
    # The chosen-topic meetup floats to the front and carries the honest suffix.
    assert feed[0].id == a_bball.id
    assert feed[0].rec_reason.endswith("matches your chosen topics")
    assert getattr(feed[0], "rec_topic_match", False) is True
    # Cold start (no interests) still produces a base reason; topic suffix appends to it.
    assert feed[0].rec_reason.startswith("soonest first")
    # The non-topic meetup is still present (never hidden) and is NOT mislabelled.
    chess_card = next(a for a in feed if a.id != a_bball.id)
    assert "chosen topics" not in chess_card.rec_reason


def test_topic_nudge_reorders_child_feed_without_leaking_adult_cohort():
    # Non-vacuous: the child's OWN-cohort feed is populated, so the nudge actually runs over a
    # non-empty list — and the adult-cohort activity must still never appear.
    adult_owner = _user("tp-ao")
    sport, tabletop, bball, chess = _types()
    adult_bball = _activity(adult_owner, bball, "Adults-only by cohort")

    child_owner = _user("tp-cown", band=AgeBand.UNDER_16)
    ParentalConsent.objects.create(
        minor=child_owner, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    child_bball = _activity(child_owner, bball, "Kids hoops")  # CHILD cohort + sport topic

    viewer = _user("tp-cview", band=AgeBand.UNDER_16)
    services.set_topic_preferences(viewer, ["tp-sport"])
    feed = services.recommended_with_reasons(viewer, limit=10)
    ids = [a.id for a in feed]

    assert feed, "child feed must be non-empty for this test to be meaningful"
    assert child_bball.id in ids  # the child's own-cohort meetup IS shown
    assert adult_bball.id not in ids  # the adult-cohort meetup is NEVER leaked in
    # Prove the nudge actually executed (guards against regressing to a vacuous empty-feed test).
    shown = next(a for a in feed if a.id == child_bball.id)
    assert shown.rec_reason.endswith("matches your chosen topics")


def test_choosing_topics_adds_no_per_activity_query():
    # Regression for the N+1: the topic-match ancestry walk must read preloaded category data, so
    # choosing topics adds only a small per-FEED constant — never one query per card.
    from django.db import connection
    from django.test.utils import CaptureQueriesContext

    me = _user("tp-nplus")
    _types()
    owner = _user("tp-nplus-owner")
    for i in range(6):
        _activity(owner, ActivityType.objects.get(slug="tp-bball"), f"Hoops {i}")

    with CaptureQueriesContext(connection) as base:  # no topics -> ancestry walk never runs
        feed_a = list(services.recommended_with_reasons(me, limit=10))
    services.set_topic_preferences(me, ["tp-sport"])
    with CaptureQueriesContext(connection) as withtopics:
        feed_b = list(services.recommended_with_reasons(me, limit=10))

    assert len(feed_a) == 6 and len(feed_b) == 6  # same 6-activity cold-start feed both times
    # Without the select_related preload this delta scales with the 6 activities (≈ +24);
    # with it, only the prefs lookup remains.
    assert len(withtopics.captured_queries) <= len(base.captured_queries) + 3, (
        len(base.captured_queries),
        len(withtopics.captured_queries),
    )


# --- DRF parity (mirrors InterestsView) ----------------------------------------------------


def test_topics_api_get_put_and_ignored():
    me = _user("tp-api")
    _types()
    client = APIClient()
    client.force_authenticate(me)

    put = client.put(
        "/api/recommendations/topics/", {"topics": ["tp-sport", "nope"]}, format="json"
    )
    assert put.status_code == 200, put.content
    assert put.json()["topics"] == ["tp-sport"]
    assert put.json()["ignored"] == ["nope"]

    got = client.get("/api/recommendations/topics/")
    assert got.status_code == 200
    assert got.json()["topics"] == ["tp-sport"]

    # Bad payload type -> 400.
    assert (
        client.put("/api/recommendations/topics/", {"topics": "sport"}, format="json").status_code
        == 400
    )


def test_topics_api_requires_auth():
    assert APIClient().get("/api/recommendations/topics/").status_code in (401, 403)
