"""Web tests for wave-7: F15 (access facts + preference) + F16 (a11y chrome + text list)."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import AccessPreference, Place

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _place(name="Town Library", raw_tags=None, lon=23.6, lat=46.77):
    return Place.objects.create(
        name=name,
        location=Point(lon, lat, srid=4326),
        source=Place.Source.OSM,
        raw_tags=raw_tags or {},
    )


def _client(user=None):
    c = Client()
    if user:
        c.force_login(user)
    return c


# --- F15: accessibility facts + preference ---------------------------------------------


def test_place_detail_shows_honest_facts():
    place = _place(raw_tags={"wheelchair": "yes"})
    body = _client(_user("a15a")).get(f"/places/{place.id}/").content.decode()
    assert "Step-free access" in body
    assert "not recorded" in body  # the un-tagged facts render honestly, not as "yes"


def test_unknown_accessibility_not_claimed():
    place = _place(raw_tags={})  # nothing recorded
    body = _client(_user("a15b")).get(f"/places/{place.id}/").content.decode()
    assert "not recorded" in body
    assert "fact--true" not in body  # never a green "yes" badge on unknown data


def test_access_preference_save_and_match_badge():
    user = _user("a15c")
    c = _client(user)
    assert c.get("/access/").status_code == 200
    resp = c.post("/access/", {"needs_step_free": "on"})
    assert resp.status_code == 302
    assert AccessPreference.objects.get(user=user).needs_step_free is True
    # A step-free venue now shows the match badge for this user.
    place = _place(raw_tags={"wheelchair": "yes"})
    body = c.get(f"/places/{place.id}/").content.decode()
    assert "Matches your access needs" in body


def test_place_detail_anonymous_does_not_error():
    place = _place(raw_tags={"wheelchair": "yes"})
    resp = _client().get(f"/places/{place.id}/")
    assert resp.status_code == 200  # pref/access_match bound even when anonymous


# --- F16: a11y chrome + JS-free places list --------------------------------------------


def test_base_has_skip_link_and_main_landmark():
    body = _client().get("/").content.decode()
    assert "Skip to main content" in body
    assert 'id="main"' in body


def test_places_list_renders_server_side_without_js():
    _place(name="Cluj Central Park")
    body = _client().get("/places/list/").content.decode()
    # Server-rendered: the place name is in the HTML itself (no API/JS fetch needed).
    assert "Cluj Central Park" in body


def test_places_list_is_public():
    assert _client().get("/places/list/").status_code == 200


def test_places_list_shows_accessibility_badge():
    _place(name="Step-free Hall", raw_tags={"wheelchair": "yes"})
    body = _client().get("/places/list/").content.decode()
    assert "Step-free Hall" in body
    assert "Step-free access" in body  # F15 badge composed onto the F16 list


def test_places_list_city_filter():
    Place.objects.create(
        name="Elsewhere",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        address_city="Bucharest",
    )
    Place.objects.create(
        name="Local Spot",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
    )
    body = _client().get("/places/list/?city=Cluj-Napoca").content.decode()
    assert "Local Spot" in body
    assert "Elsewhere" not in body
