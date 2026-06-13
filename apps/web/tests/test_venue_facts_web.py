"""F19 (web) — venue facts on place_detail + the member vote flow."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceFactVote

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _place(raw_tags=None):
    return Place.objects.create(
        name="Library", location=PT, source=Place.Source.OSM, raw_tags=raw_tags or {}
    )


def test_place_detail_shows_venue_facts_and_vote_form():
    place = _place({})  # OSM silent -> crowd vote form shown to a verified member
    body = _client(_user("vf1")).get(f"/places/{place.pk}/").content.decode()
    assert "What&#x27;s at this venue" in body or "What's at this venue" in body
    assert f"/places/{place.pk}/facts/vote/" in body  # vote form present


def test_member_can_vote_a_fact():
    place = _place({})
    voter = _user("vf2")
    resp = _client(voter).post(
        f"/places/{place.pk}/facts/vote/",
        {"fact_key": PlaceFactVote.FactKey.TOILETS, "value": "yes"},
    )
    assert resp.status_code == 302
    row = PlaceFactVote.objects.get(place=place, user=voter, fact_key="toilets")
    assert row.value is True


def test_osm_sourced_fact_hides_vote_form():
    # A fact decided by map data shows "from map data" and NO crowd vote form for that fact (its
    # per-fact hidden input is absent), while a crowd-only fact (indoor_shelter) still has its form.
    place = _place({"toilets": "yes"})
    body = _client(_user("vf3")).get(f"/places/{place.pk}/").content.decode()
    assert "from map data" in body
    assert 'name="fact_key" value="toilets"' not in body  # OSM-sourced -> no vote form
    assert 'name="fact_key" value="indoor_shelter"' in body  # crowd-only -> form present


def test_vote_buttons_disable_after_voting():
    place = _place({})  # indoor_shelter is crowd-only -> votable
    voter = _user("vf5")
    c = _client(voter)
    c.post(
        f"/places/{place.pk}/facts/vote/",
        {"fact_key": PlaceFactVote.FactKey.INDOOR_SHELTER, "value": "yes"},
    )
    body = c.get(f"/places/{place.pk}/").content.decode()
    assert 'value="yes" disabled' in body  # the side you voted is disabled
    assert 'value="no" disabled' not in body  # the other side stays enabled


def test_kid_badge_shows_when_confirmed():
    place = _place({"toilets": "yes"})  # a kid-relevant fact confirmed by OSM
    body = _client(_user("vf4")).get(f"/places/{place.pk}/").content.decode()
    assert "kid-friendly facilities" in body
