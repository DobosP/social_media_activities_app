import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name, band=AgeBand.ADULT):
    user = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(user, AssuranceResult(age_band=band, provider="dev"))
    return user


def _type(slug="web-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="web-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def test_landing_is_public():
    assert Client().get("/").status_code == 200


def test_register_logs_in_and_home_renders():
    resp = Client().post(
        "/register/",
        {"username": "newbie", "display_name": "Newbie", "password": PW, "age_band": AgeBand.ADULT},
    )
    assert resp.status_code == 302
    user = User.objects.get(username="newbie")
    assert user.cohort == "adult"
    c = _client(user)
    assert c.get("/").status_code == 200


@pytest.mark.parametrize(
    "path", ["/places/", "/activities/", "/interests/", "/profile/", "/notifications/", "/donate/"]
)
def test_core_pages_render(path):
    assert _client(_user("pages")).get(path).status_code == 200


def test_create_activity_and_view_detail():
    owner = _user("web-owner")
    place, atype = _place(), _type()
    c = _client(owner)
    resp = c.post(
        "/activities/new/",
        {
            "place": place.id,
            "activity_type": atype.id,
            "title": "Web game",
            "description": "",
            "starts_at": "2030-01-01T10:00",
            "ends_at": "",
            "capacity": "",
        },
    )
    assert resp.status_code == 302
    detail = c.get(resp.headers["Location"])
    assert detail.status_code == 200
    assert b"Web game" in detail.content


def test_join_then_owner_vote_admits_member():
    owner = _user("web-o2")
    activity = create_activity(
        owner, place=_place(), activity_type=_type(), title="Game", starts_at="2030-02-01T10:00Z"
    )
    joiner = _user("web-joiner")
    jc = _client(joiner)
    assert jc.post(f"/activities/{activity.id}/join/").status_code == 302
    membership = Membership.objects.get(activity=activity, user=joiner)
    assert membership.state == Membership.State.REQUESTED

    # Owner is the only voting member; one approval clears the 2/3 threshold.
    oc = _client(owner)
    oc.post(f"/activities/{activity.id}/members/{membership.id}/vote/", {"vote": "approve"})
    membership.refresh_from_db()
    assert membership.state == Membership.State.MEMBER


def test_interests_save_and_show_on_profile():
    user = _user("web-interests")
    _type(slug="web-chess")
    c = _client(user)
    assert c.post("/interests/", {"interests": ["web-chess"]}).status_code == 302
    profile = c.get("/profile/")
    assert profile.status_code == 200


def test_cohort_isolation_detail_404_for_other_cohort():
    owner = _user("web-adult")
    activity = create_activity(
        owner, place=_place(), activity_type=_type(), title="Adults", starts_at="2030-03-01T10:00Z"
    )
    child = _user("web-child", band=AgeBand.UNDER_16)
    assert _client(child).get(f"/activities/{activity.id}/").status_code == 404


def test_donate_redirects_to_checkout():
    resp = _client(_user("web-donor")).post("/donate/", {"amount": "10"})
    assert resp.status_code == 302


def test_events_pages_render():
    from datetime import timedelta

    from django.utils import timezone

    from apps.events.models import Event

    place, atype = _place(), _type(slug="web-ev")
    event = Event.objects.create(
        place=place,
        activity_type=atype,
        title="City Festival",
        starts_at=timezone.now() + timedelta(days=3),
    )
    c = _client(_user("ev-user"))
    assert c.get("/events/").status_code == 200
    assert c.get(f"/events/{event.id}/").status_code == 200


def test_verify_age_flow_sets_band():
    # A fresh, unverified user proves "adult" via the sandbox EU wallet.
    user = User.objects.create_user(username="unverified", password=PW)
    c = Client()
    c.force_login(user)
    assert c.get("/verify-age/").status_code == 200
    resp = c.post("/verify-age/", {"age": "adult"})
    assert resp.status_code == 302
    user.refresh_from_db()
    assert user.is_identity_verified is True
    assert user.cohort == "adult"


def test_wards_page_renders():
    assert _client(_user("guardian-user")).get("/wards/").status_code == 200


def test_report_activity_creates_report():
    owner = _user("rep-owner")
    activity = create_activity(
        owner,
        place=_place(),
        activity_type=_type(slug="web-rep"),
        title="ReportMe",
        starts_at="2030-05-01T10:00Z",
    )
    reporter = _user("reporter")  # same (adult) cohort -> can see/report
    c = _client(reporter)
    assert c.get(f"/report/?type=activity&id={activity.id}").status_code == 200
    resp = c.post(
        "/report/", {"type": "activity", "id": activity.id, "reason": "spam", "detail": "x"}
    )
    assert resp.status_code == 302
    from apps.safety.models import Report

    assert Report.objects.filter(reason="spam").exists()


def test_block_then_unblock_user():
    me, other = _user("blk-me"), _user("blk-other")
    from apps.safety.models import Block

    c = _client(me)
    assert c.post(f"/users/{other.id}/block/").status_code == 302
    assert Block.objects.filter(blocker=me, blocked=other).exists()
    assert c.post(f"/users/{other.id}/unblock/").status_code == 302
    assert not Block.objects.filter(blocker=me, blocked=other).exists()
