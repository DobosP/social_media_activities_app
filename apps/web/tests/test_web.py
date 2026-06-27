from unittest import mock

import pytest
from django.contrib.gis.geos import Point
from django.core.cache import cache
from django.test import Client

from apps.accounts.identity.base import AssuranceResult, IdentityVerificationError
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


def test_place_detail_renders_attribution_credit():
    owner = _user("web-place-credit")
    place = Place.objects.create(
        name="RO-EDU Hall",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.ROEDU,
        external_id="hall-1",
        attribution="RO-EDU",
        license_name="CC BY 4.0",
        provenance_url="https://data.example/venues/hall-1",
    )

    resp = _client(owner).get(f"/places/{place.pk}/")
    assert resp.status_code == 200
    body = resp.content.decode()
    assert "Source credit:" in body
    assert "RO-EDU" in body
    assert "CC BY 4.0" in body


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


def test_profile_does_not_leak_another_users_progression():
    other = _user("web-progression-other")
    viewer = _user("web-progression-viewer")
    c = _client(viewer)

    other_profile = c.get(f"/profile/{other.pk}/")
    assert other_profile.status_code == 404
    assert other.username.encode() not in other_profile.content
    assert b"progression" not in other_profile.content
    assert b"intensity" not in other_profile.content

    profile = c.get("/profile/")

    assert profile.status_code == 200
    body = profile.content.decode()
    assert other.username not in body
    assert f"/profile/{other.pk}/" not in body
    assert "progression_level" not in body
    assert "progression_intensity" not in body
    assert "intensity" not in body


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


def test_messages_page_renders():
    resp = _client(_user("messenger")).get("/messages/")
    assert resp.status_code == 200
    assert b"mz-config" in resp.content
    assert b"end-to-end encrypted" in resp.content


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


# --- Transparency pages & GDPR self-service deletion --------------------------------


@pytest.mark.parametrize("path", ["/privacy/", "/terms/"])
def test_transparency_pages_are_public(path):
    resp = Client().get(path)
    assert resp.status_code == 200
    # Must clearly flag the copy as not-yet-finalised legal text.
    assert b"DRAFT" in resp.content


def test_footer_links_to_privacy_and_terms():
    resp = Client().get("/")
    assert resp.status_code == 200
    assert b'href="/privacy/"' in resp.content
    assert b'href="/terms/"' in resp.content


def test_account_delete_requires_login():
    # Anonymous POST is redirected to login, not processed.
    resp = Client().post("/account/delete/")
    assert resp.status_code == 302
    assert "/login/" in resp.headers["Location"]


def test_account_delete_get_shows_preview():
    # F33: GET is now the honest counts-only erasure-preview confirmation step (was 405 when the
    # view was @require_POST). The irreversible erase still happens only on POST.
    resp = _client(_user("del-get")).get("/account/delete/")
    assert resp.status_code == 200
    assert "What gets permanently deleted" in resp.content.decode()


def test_account_delete_erases_and_logs_out():
    user = _user("del-me")
    c = _client(user)
    with mock.patch("apps.accounts.services.erase_user", create=True) as erase:
        resp = c.post("/account/delete/")
        erase.assert_called_once_with(user, user)
    assert resp.status_code == 302
    assert resp.headers["Location"] == "/"
    # Session was cleared by logout(): a follow-up request is anonymous.
    home = c.get("/")
    assert home.wsgi_request.user.is_authenticated is False


def test_account_delete_failure_shows_error_and_stays():
    user = _user("del-fail")
    c = _client(user)
    with mock.patch(
        "apps.accounts.services.erase_user", create=True, side_effect=PermissionError("nope")
    ):
        resp = c.post("/account/delete/")
    # Redirected back to profile; user is still logged in.
    assert resp.status_code == 302
    assert resp.headers["Location"] == "/profile/"
    assert c.get("/profile/").status_code == 200


def test_delete_control_lives_in_settings_and_profile_points_there():
    # W3 moved the account danger zone to /settings/ (per user request — the profile was
    # overloaded). The control must exist there, and the profile must point at it.
    c = _client(_user("del-control"))
    settings_page = c.get("/settings/")
    assert settings_page.status_code == 200
    assert b"Delete my account" in settings_page.content
    # F33: the control is now a GET link to the erasure-preview page (was a direct POST form).
    assert b'href="/account/delete/"' in settings_page.content
    profile = c.get("/profile/")
    assert profile.status_code == 200
    assert b"/settings/" in profile.content


# --- Auth hardening regressions -----------------------------------------------------


def test_login_locks_out_after_repeated_failures(settings):
    # Tight limit so the test stays fast; LocMemCache persists in-process, so clear it.
    settings.LOGIN_FAILURE_LIMIT = 3
    settings.LOGIN_FAILURE_WINDOW_SECONDS = 900
    cache.clear()
    _user("lockme")
    c = Client()
    for _ in range(settings.LOGIN_FAILURE_LIMIT):
        resp = c.post("/login/", {"username": "lockme", "password": "wrong"})
        assert resp.status_code == 200  # re-rendered form, not authenticated

    # Even the *correct* password is now refused: the (username, IP) pair is locked out.
    resp = c.post("/login/", {"username": "lockme", "password": PW})
    assert resp.status_code == 200
    assert b"Too many failed login attempts" in resp.content
    assert "_auth_user_id" not in c.session


def test_login_succeeds_before_lockout_and_clears_counter(settings):
    settings.LOGIN_FAILURE_LIMIT = 5
    settings.LOGIN_FAILURE_WINDOW_SECONDS = 900
    cache.clear()
    _user("goodlogin")
    c = Client()
    # A couple of failures, still under the limit.
    c.post("/login/", {"username": "goodlogin", "password": "wrong"})
    c.post("/login/", {"username": "goodlogin", "password": "wrong"})
    # Correct credentials authenticate and reset the failure counter.
    resp = c.post("/login/", {"username": "goodlogin", "password": PW})
    assert resp.status_code == 302
    assert "_auth_user_id" in c.session


def test_register_rolls_back_on_identity_error(settings, monkeypatch):
    # If the identity provider raises, no orphan account must remain and no 500 occurs.
    cache.clear()
    from apps.web import views as web_views

    class _Boom:
        def verify(self, user, **kwargs):
            raise IdentityVerificationError("wallet unavailable")

    monkeypatch.setattr(web_views, "get_identity_provider", lambda: _Boom())
    resp = Client().post(
        "/register/",
        {
            "username": "orphan",
            "display_name": "Orphan",
            "password": PW,
            "age_band": AgeBand.ADULT,
        },
    )
    assert resp.status_code == 302  # redirected, not a 500
    assert not User.objects.filter(username="orphan").exists()


def test_block_redirect_rejects_offsite_next():
    me, other = _user("redir-me"), _user("redir-other")
    c = _client(me)
    resp = c.post(f"/users/{other.id}/block/", {"next": "https://evil.example/phish"})
    assert resp.status_code == 302
    # Open redirect blocked: falls back to the on-site default instead of the evil host.
    assert "evil.example" not in resp.headers["Location"]
    assert resp.headers["Location"] == "/"


def test_block_redirect_allows_safe_relative_next():
    me, other = _user("redir2-me"), _user("redir2-other")
    c = _client(me)
    resp = c.post(f"/users/{other.id}/block/", {"next": "/activities/"})
    assert resp.status_code == 302
    assert resp.headers["Location"] == "/activities/"


def test_unblock_redirect_rejects_offsite_next():
    me, other = _user("redir3-me"), _user("redir3-other")
    c = _client(me)
    resp = c.post(f"/users/{other.id}/unblock/", {"next": "//evil.example/x"})
    assert resp.status_code == 302
    assert "evil.example" not in resp.headers["Location"]
    assert resp.headers["Location"] == "/profile/"  # safe on-site default (reversed URL)
