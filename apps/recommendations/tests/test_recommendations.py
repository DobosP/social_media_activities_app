import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.recommendations import services
from apps.recommendations.models import ActivityEmbedding
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _types():
    sport = ActivityCategory.objects.create(slug="rec-sport", name="Sport")
    tabletop = ActivityCategory.objects.create(slug="rec-table", name="Tabletop")
    bball = ActivityType.objects.create(slug="rec-bball", name="Basketball", category=sport)
    chess = ActivityType.objects.create(slug="rec-chess", name="Chess", category=tabletop)
    return bball, chess


def _activity(owner, atype, title="A"):
    place = Place.objects.create(
        name="P", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title=title, starts_at="2030-01-01T10:00Z"
    )


def test_signal_creates_embedding_on_activity_create():
    owner = _user("rec-o0")
    bball, _ = _types()
    activity = _activity(owner, bball, "Hoops")
    assert ActivityEmbedding.objects.filter(activity=activity).exists()


def test_recommendations_rank_by_declared_interest():
    owner = _user("rec-owner")
    me = _user("rec-me")
    bball, chess = _types()
    a_bball = _activity(owner, bball, "Basketball game")
    _activity(owner, chess, "Chess night")

    services.set_interests(me, ["rec-bball"])
    recs = services.recommend_activities(me, limit=10)

    assert recs, "expected recommendations"
    assert recs[0].id == a_bball.id  # the basketball meetup matches the declared interest


def test_recommendations_are_cohort_scoped():
    adult_owner = _user("rec-ao")
    child = _user("rec-child", band=AgeBand.UNDER_16)
    bball, _ = _types()
    adult_activity = _activity(adult_owner, bball, "Adults only by cohort")

    services.set_interests(child, ["rec-bball"])
    recs = services.recommend_activities(child, limit=10)
    assert adult_activity.id not in [r.id for r in recs]


def test_cold_start_falls_back_to_upcoming():
    owner = _user("rec-o3")
    me = _user("rec-m3")
    bball, _ = _types()
    upcoming = _activity(owner, bball, "Soon")

    recs = services.recommend_activities(me, limit=10)  # me has no interests/history
    assert upcoming.id in [r.id for r in recs]


def test_interests_api_roundtrip():
    me = _user("rec-api")
    _types()
    client = APIClient()
    client.force_authenticate(me)

    resp = client.put(
        "/api/recommendations/interests/",
        {"interests": ["rec-bball", "does-not-exist"]},
        format="json",
    )
    assert resp.status_code == 200
    assert resp.json()["interests"] == ["rec-bball"]
    assert "does-not-exist" in resp.json()["ignored"]

    assert client.get("/api/recommendations/interests/").json()["interests"] == ["rec-bball"]


def test_recommendations_api_returns_scored_results():
    owner = _user("rec-o4")
    me = _user("rec-m4")
    bball, _ = _types()
    _activity(owner, bball, "Scored game")

    client = APIClient()
    client.force_authenticate(me)
    client.put("/api/recommendations/interests/", {"interests": ["rec-bball"]}, format="json")
    resp = client.get("/api/recommendations/activities/")

    assert resp.status_code == 200
    results = resp.json()["results"]
    assert results and results[0]["title"] == "Scored game"
    assert "match_score" in results[0]
