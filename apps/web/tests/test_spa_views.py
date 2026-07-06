"""ADR-0016 SPA plumbing: kill switch, shell rendering, soft-nav JSON, card contract.

The SOCIAL_REACT_UI flag is False in test settings, so the whole legacy suite keeps
asserting SSR output; these tests flip it per-case via override_settings.
"""

import json
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client, override_settings
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType
from apps.web.views_spa import activity_card

pytestmark = pytest.mark.django_db

PW = "pw-123-secret"


def _user(name="spa-user"):
    user = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


def _client(user):
    client = Client()
    client.force_login(user)
    return client


def _activity(owner, title="SPA pickup"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="spa-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="spa-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="SPA Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title=title,
        starts_at=timezone.now() + timedelta(days=1),
    )


def test_flag_off_serves_legacy_templates():
    user = _user()
    client = _client(user)
    for url, template in [
        ("/", "web/home.html"),
        ("/activities/", "web/activities.html"),
        ("/organize/", "web/organize.html"),
    ]:
        response = client.get(url)
        assert response.status_code == 200
        assert template in [t.name for t in response.templates], url
        assert "web/spa.html" not in [t.name for t in response.templates], url


@override_settings(SOCIAL_REACT_UI=True)
def test_flag_on_serves_spa_shell_with_bootstrap_island():
    user = _user()
    _activity(user)
    client = _client(user)
    response = client.get("/activities/")
    assert response.status_code == 200
    assert "web/spa.html" in [t.name for t in response.templates]
    html = response.content.decode()
    assert 'id="spa-bootstrap"' in html
    assert 'data-route="browse"' in html


@override_settings(SOCIAL_REACT_UI=True)
def test_flag_on_anonymous_home_stays_landing():
    response = Client().get("/")
    assert response.status_code == 200
    assert "web/landing.html" in [t.name for t in response.templates]


@override_settings(SOCIAL_REACT_UI=True)
def test_soft_nav_returns_json_payload():
    user = _user()
    activity = _activity(user)
    client = _client(user)

    response = client.get("/activities/", {"_data": "1"})
    assert response["Content-Type"].startswith("application/json")
    payload = json.loads(response.content)
    assert payload["route"] == "browse"
    assert payload["title"]
    assert payload["csrf"]
    cards = payload["data"]["cards"]
    assert [c["pk"] for c in cards] == [activity.pk]
    assert payload["data"]["page"]["count"] == 1

    home = json.loads(client.get("/", {"_data": "1"}).content)
    assert home["route"] == "home"
    assert {"sections", "starterTypes", "events", "ui", "urls"} <= set(home["data"])

    organize = json.loads(client.get("/organize/", {"_data": "1"}).content)
    assert organize["route"] == "organize"
    assert organize["data"]["activities"][0]["pk"] == activity.pk


def test_activity_card_contract_without_cover():
    user = _user()
    activity = _activity(user, title="Card contract")
    card = activity_card(activity, user)
    assert card["pk"] == activity.pk
    assert card["url"] == f"/activities/{activity.pk}/"
    assert card["title"] == "Card contract"
    # No cover uploaded -> deterministic generated accent (ADR-0007 fallback).
    assert card["visual"]["kind"] == "accent"
    assert card["visual"]["svg"].startswith("<svg")
    assert card["tags"][0] == "Basketball"
    assert "·" in card["meta"]
    assert card["score"] is None


@override_settings(SOCIAL_REACT_UI=True)
def test_public_events_spa_keeps_seo_and_snapshot():
    from apps.events.models import Event

    owner = _user("spa-seo-user")
    activity = _activity(owner)
    event = Event.objects.create(
        title="Saturday spa football",
        starts_at=timezone.now() + timedelta(days=3),
        place=activity.place,
        activity_type=activity.activity_type,
        source=Event.Source.MANUAL,
    )

    response = Client().get("/events/")  # anonymous
    assert response.status_code == 200
    html = response.content.decode()
    assert "web/spa.html" in [t.name for t in response.templates]
    # Crawler/noscript snapshot inside #root: real title + link before hydration.
    assert "Saturday spa football" in html
    # JSON-LD ItemList survives the SPA shell.
    assert 'type="application/ld+json"' in html
    assert '"ItemList"' in html
    assert 'rel="alternate" type="application/rss+xml"' in html

    payload = json.loads(Client().get("/events/", {"_data": "1"}).content)
    assert payload["route"] == "events"
    assert payload["csrf"] == ""  # public payload: cacheable, no token
    assert payload["data"]["events"][0]["pk"] == event.pk

    # Filtered result pages stay out of the index, exactly like the legacy page.
    filtered = Client().get("/events/", {"q": "football"}).content.decode()
    assert "noindex, follow" in filtered


@override_settings(SOCIAL_REACT_UI=True)
def test_public_places_spa_lists_and_json():
    owner = _user("spa-places-user")
    _activity(owner)  # creates the Place

    response = Client().get("/places/list/")
    assert response.status_code == 200
    html = response.content.decode()
    assert 'data-route="places"' in html
    assert "SPA Court" in html  # snapshot content

    payload = json.loads(Client().get("/places/list/", {"_data": "1"}).content)
    assert payload["route"] == "places"
    assert payload["data"]["places"][0]["name"] == "SPA Court"


@override_settings(SOCIAL_REACT_UI=True)
def test_public_things_index_spa():
    response = Client().get("/things-to-do/")
    assert response.status_code == 200
    html = response.content.decode()
    assert 'data-route="things-index"' in html
    payload = json.loads(Client().get("/things-to-do/", {"_data": "1"}).content)
    assert payload["route"] == "things-index"
    assert "cities" in payload["data"]
