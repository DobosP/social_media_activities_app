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


# --- F5: distance-bounded recommendations (request-only proximity, CORE only) ---------

CLUJ = (23.60, 46.77)
FAR = (23.85, 46.77)  # ~19 km east of CLUJ


def _place_at(lon, lat, raw_tags=None):
    return Place.objects.create(
        name="P",
        location=Point(lon, lat, srid=4326),
        source=Place.Source.OSM,
        raw_tags=raw_tags or {},  # raw_tags is NOT NULL; {} => all-unknown accessibility facts
    )


def _act_at(owner, atype, place, title="A"):
    return create_activity(
        owner, place=place, activity_type=atype, title=title, starts_at="2030-01-01T10:00Z"
    )


def test_no_coords_byte_identical():
    owner, me = _user("rec-bi-o"), _user("rec-bi-me")
    bball, chess = _types()
    _activity(owner, bball, "B")
    _activity(owner, chess, "C")
    services.set_interests(me, ["rec-bball"])
    a = [r.id for r in services.recommend_activities(me, limit=10)]
    b = [r.id for r in services.recommend_activities(me, limit=10, near_point=None, radius_m=None)]
    assert a and a == b  # same ids AND order with vs without the (absent) coords


def test_distance_decay_prioritizes_near():
    owner, me = _user("rec-d-o"), _user("rec-d-me")
    bball, _ = _types()
    near = _act_at(owner, bball, _place_at(*CLUJ), "Near")  # identical cosine (same type)
    far = _act_at(owner, bball, _place_at(*FAR), "Far")
    services.set_interests(me, ["rec-bball"])
    pt = Point(*CLUJ, srid=4326)
    ids = [r.id for r in services.recommend_activities(me, limit=10, near_point=pt, radius_m=50000)]
    assert ids.index(near.id) < ids.index(far.id)  # equal cosine -> decay puts the nearer first


def test_radius_still_hard_filters():
    owner, me = _user("rec-r-o"), _user("rec-r-me")
    bball, _ = _types()
    far = _act_at(owner, bball, _place_at(*FAR), "Far")
    services.set_interests(me, ["rec-bball"])
    pt = Point(*CLUJ, srid=4326)
    ids = [r.id for r in services.recommend_activities(me, limit=10, near_point=pt, radius_m=5000)]
    assert far.id not in ids  # ~19 km is outside 5 km -> hard-excluded, decay can't resurrect it


def test_access_match_boost_lifts_matching_venue():
    from apps.places.services import set_access_preference

    owner, me = _user("rec-a-o"), _user("rec-a-me")
    bball, _ = _types()
    step_free = _act_at(owner, bball, _place_at(*CLUJ, raw_tags={"wheelchair": "yes"}), "Step-free")
    unknown = _act_at(owner, bball, _place_at(*CLUJ), "Unknown")  # same type+coords; boost decides
    services.set_interests(me, ["rec-bball"])
    set_access_preference(me, needs_step_free=True)
    pt = Point(*CLUJ, srid=4326)
    ids = [r.id for r in services.recommend_activities(me, limit=10, near_point=pt, radius_m=50000)]
    assert ids.index(step_free.id) < ids.index(unknown.id)  # soft additive boost lifts the match


def test_unknown_accessibility_never_hidden():
    from apps.places.services import set_access_preference

    owner, me = _user("rec-u-o"), _user("rec-u-me")
    bball, _ = _types()
    unknown = _act_at(owner, bball, _place_at(*CLUJ), "Unknown")
    services.set_interests(me, ["rec-bball"])
    set_access_preference(me, needs_step_free=True)
    pt = Point(*CLUJ, srid=4326)
    ids = [r.id for r in services.recommend_activities(me, limit=10, near_point=pt, radius_m=50000)]
    assert unknown.id in ids  # additive-only boost never hides an unknown-accessibility venue (F15)


def test_rec_score_distance_monotone_and_clamped():
    s = services._rec_score
    # Positive cosine: a nearer venue strictly outscores a farther one at equal similarity.
    assert s(0.2, 100, False) > s(0.2, 19000, False)
    # Negative cosine (cosine_distance > 1): clamped to 0 -> never inverts (far can't beat near).
    assert s(1.6, 100, False) == 0.0
    assert s(1.6, 100, False) >= s(1.6, 19000, False)
    # Access boost is additive-only.
    assert s(0.2, 100, True) > s(0.2, 100, False)


def test_near_suffix_flag_only_within_threshold():
    owner, me = _user("rec-n-o"), _user("rec-n-me")
    bball, _ = _types()
    near = _act_at(owner, bball, _place_at(*CLUJ), "Near")  # 0 m
    far = _act_at(owner, bball, _place_at(*FAR), "Far")  # ~19 km (within the 50 km radius)
    services.set_interests(me, ["rec-bball"])
    pt = Point(*CLUJ, srid=4326)
    by_id = {
        r.id: r for r in services.recommend_activities(me, limit=10, near_point=pt, radius_m=50000)
    }
    assert by_id[near.id].rec_near is True
    assert by_id[far.id].rec_near is False  # within radius + decayed, but NOT "near you" (>2 km)


def test_rec_distance_stays_cosine_with_coords():
    owner, me = _user("rec-c-o"), _user("rec-c-me")
    bball, _ = _types()
    a = _act_at(owner, bball, _place_at(*CLUJ), "A")
    services.set_interests(me, ["rec-bball"])
    pt = Point(*CLUJ, srid=4326)
    with_coords = {
        r.id: r.rec_distance
        for r in services.recommend_activities(me, limit=10, near_point=pt, radius_m=50000)
    }
    no_coords = {r.id: r.rec_distance for r in services.recommend_activities(me, limit=10)}
    # rec_distance is the RAW cosine in BOTH modes (never the blended sort score) -> % match honest.
    assert with_coords[a.id] == no_coords[a.id]
    assert 0.0 <= float(with_coords[a.id]) <= 2.0


def test_cohort_isolation_holds_with_coords():
    adult_owner = _user("rec-ci-o")
    child = _user("rec-ci-c", band=AgeBand.UNDER_16)
    bball, _ = _types()
    adult_activity = _act_at(adult_owner, bball, _place_at(*CLUJ), "Adult")
    services.set_interests(child, ["rec-bball"])
    pt = Point(*CLUJ, srid=4326)
    ids = [
        r.id for r in services.recommend_activities(child, limit=10, near_point=pt, radius_m=50000)
    ]
    assert adult_activity.id not in ids


def test_cold_start_unaffected_by_coords():
    owner, me = _user("rec-cs-o"), _user("rec-cs-me")
    bball, _ = _types()
    a = _act_at(owner, bball, _place_at(*CLUJ), "Soon")
    pt = Point(*CLUJ, srid=4326)
    recs = services.recommend_activities(
        me, limit=10, near_point=pt, radius_m=50000
    )  # no interests
    assert a.id in [r.id for r in recs]
    assert all(not hasattr(r, "rec_distance") for r in recs)  # cold-start path: no vector scoring


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
