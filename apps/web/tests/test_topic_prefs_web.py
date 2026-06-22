"""Web surfaces for topic preferences (self-service /topics/ + guardian /wards/<pk>/topics/) and
the two TEXT-FIRST browse modes (?view=list|card) on the activities list.

Invariant guards baked in: the browse modes never render an <img>, and a non-guardian can never
set a ward's feed."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import Place
from apps.recommendations import services as recs
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _teen(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _topics():
    sport = ActivityCategory.objects.create(slug="tpw-sport", name="Sport")
    reading = ActivityCategory.objects.create(slug="tpw-read", name="Reading")
    bball = ActivityType.objects.create(slug="tpw-bball", name="Basketball", category=sport)
    return sport, reading, bball


def _activity(owner, atype, title):
    place = Place.objects.create(
        name="P", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title=title, starts_at="2030-01-01T10:00Z"
    )


# --- self-service /topics/ -----------------------------------------------------------------


def test_topics_page_renders_and_saves():
    user = _adult("tpw-self")
    _topics()
    c = _client(user)

    page = c.get("/topics/")
    assert page.status_code == 200
    body = page.content.decode()
    assert "Your topics" in body
    assert 'value="tpw-sport"' in body

    resp = c.post("/topics/", {"topics": ["tpw-sport"]})
    assert resp.status_code == 302
    assert recs.topic_preference_slugs(user) == frozenset({"tpw-sport"})
    # The saved topic is pre-checked on reload.
    assert "checked" in c.get("/topics/").content.decode()


# --- guardian control of a ward's feed -----------------------------------------------------


def test_guardian_sees_and_sets_ward_topics():
    guardian, ward = _adult("tpw-g"), _child("tpw-w")
    link_guardian(guardian, ward)
    _topics()
    c = _client(guardian)

    body = c.get("/wards/").content.decode()
    assert "Suggested topics for their feed" in body
    assert f"/wards/{ward.pk}/topics/" in body

    resp = c.post(f"/wards/{ward.pk}/topics/", {"topics": ["tpw-sport"]})
    assert resp.status_code == 302
    assert recs.topic_preference_slugs(ward) == frozenset({"tpw-sport"})


def test_non_guardian_cannot_set_ward_topics():
    guardian, ward = _adult("tpw-g2"), _child("tpw-w2")
    link_guardian(guardian, ward)
    _topics()
    recs.set_topic_preferences(ward, ["tpw-read"])

    stranger = _adult("tpw-stranger")
    resp = _client(stranger).post(f"/wards/{ward.pk}/topics/", {"topics": ["tpw-sport"]})
    # Redirects with an error, and the ward's feed is untouched (still what the guardian/ward set).
    assert resp.status_code == 302
    assert recs.topic_preference_slugs(ward) == frozenset({"tpw-read"})


def test_guardian_cannot_set_non_child_ward_topics():
    # Guardian feed-steering is CHILD-only (mirrors the F7 guardrail scope): a guardian of a teen
    # ward — or an aged-up adult whose link lingers ACTIVE — is refused and nothing is written.
    guardian, teen = _adult("tpw-tg"), _teen("tpw-teen")
    link_guardian(guardian, teen)
    _topics()
    resp = _client(guardian).post(f"/wards/{teen.pk}/topics/", {"topics": ["tpw-sport"]})
    assert resp.status_code == 302
    assert recs.topic_preference_slugs(teen) == frozenset()
    # The form isn't even offered for a non-CHILD ward.
    body = _client(guardian).get("/wards/").content.decode()
    assert "Suggested topics for their feed" not in body


# --- text-first browse modes ---------------------------------------------------------------


def test_browse_modes_are_text_first_and_switchable():
    user = _adult("tpw-browse")
    _, _, bball = _topics()
    _activity(user, bball, "Hoops one")

    c = _client(user)
    # Baseline: a no-results page carries only the page chrome (incl. the single nav avatar — the
    # one allowed profile picture). The FEED itself must add zero IMAGES in every mode (the toggle
    # icons are inline <svg>, not <img>).
    chrome_imgs = c.get("/activities/?q=zzzznomatchzzz").content.decode().count("<img")
    # Default (no ?view=) is the calm List mode.
    assert 'data-view="list"' in c.get("/activities/").content.decode()
    for mode in ("list", "cards"):
        body = c.get(f"/activities/?view={mode}").content.decode()
        assert f'data-view="{mode}"' in body, mode
        assert "Hoops one" in body  # the activity is shown in every mode
        assert body.count("<img") == chrome_imgs, mode  # text-first: the feed adds no images

    # Unknown view falls back to list (never errors), and the progressive-enhancement JS is wired.
    cards_body = c.get("/activities/?view=cards").content.decode()
    assert "browse-modes.js" in cards_body
    assert 'data-view="list"' in c.get("/activities/?view=swipe").content.decode()


def test_cards_mode_renders_a_focused_deck():
    user = _adult("tpw-card")
    _, _, bball = _topics()
    _activity(user, bball, "Hoops A")
    _activity(user, bball, "Hoops B")

    body = _client(user).get("/activities/?view=cards").content.decode()
    assert 'data-view="cards"' in body
    assert "data-browse-deck" in body  # the deck container the JS drives
    assert "data-deck-next" in body and "data-deck-prev" in body  # deck navigation chrome
    assert body.count('class="browse-item') == 2  # both meetups rendered into the deck
    assert body.count("is-current") == 1  # the server marks the first card so there's no load flash
    assert "Hoops A" in body and "Hoops B" in body


def test_browse_out_of_range_page_is_safe():
    # get_page() clamps an absurd/non-int page so deep-linking can't 500.
    user = _adult("tpw-page")
    _, _, bball = _topics()
    _activity(user, bball, "Hoops")
    c = _client(user)
    assert c.get("/activities/?view=cards&page=999").status_code == 200
    assert c.get("/activities/?view=list&page=oops").status_code == 200
