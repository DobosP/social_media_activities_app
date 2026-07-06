"""Round-4 SEO: richer structured data, internal linking, meta completeness, index hygiene.

Deepens what crawlers/answer engines can extract from already-public pages and how those pages
interlink — no new data surface. Child-safety is unchanged: every list/JSON-LD here is built from
``public_places()`` / ``upcoming_events()`` rows, so no cohort activity, minor, or pending venue
can appear, and the anonymous public surfaces carry no ``/activities/`` link.
"""

import json
import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.communities.models import Area
from apps.events.models import Event
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

CITY = "Cluj-Napoca"


def _ld_blocks(html):
    blocks = re.findall(
        r'<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>', html, flags=re.DOTALL
    )
    return [json.loads(b) for b in blocks]


def _node_of_type(html, type_name):
    """The first top-level JSON-LD node whose @type matches (handles list-valued @type)."""
    for block in _ld_blocks(html):
        types = block.get("@type")
        if types == type_name or (isinstance(types, list) and type_name in types):
            return block
    return None


def _fixture(city=CITY):
    """A public OSM venue (no proposal => public) with an upcoming event, plus its Area/type."""
    area, _ = Area.objects.get_or_create(slug="r4-cluj", defaults={"city": CITY, "name": CITY})
    cat, _ = ActivityCategory.objects.get_or_create(slug="r4-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="r4-football", defaults={"name": "Football", "category": cat}
    )
    place = Place.objects.create(
        name="Central Park",
        location=Point(23.6, 46.77, srid=4326),
        address_city=city,
        source=Place.Source.OSM,
    )
    PlaceActivity.objects.create(place=place, activity=t)
    event = Event.objects.create(
        title="Saturday football",
        starts_at=timezone.now() + timedelta(days=3),
        place=place,
        activity_type=t,
        source=Event.Source.MANUAL,
    )
    return area, t, place, event


# --- 1. Richer structured data ------------------------------------------------------------


def test_place_detail_embeds_upcoming_events_in_jsonld():
    _area, _t, place, event = _fixture()
    html = Client().get(f"/places/{place.pk}/").content.decode()
    node = _node_of_type(html, "Place")
    assert node is not None, "expected a Place JSON-LD node"
    nested = node.get("event") or []
    names = [e["name"] for e in nested]
    assert event.title in names
    assert any(f"/events/{event.pk}/saturday-football/" in e["url"] for e in nested)


def test_events_list_emits_itemlist():
    _area, _t, _place, event = _fixture()
    html = Client().get("/events/").content.decode()
    lst = _node_of_type(html, "ItemList")
    assert lst is not None, "expected an ItemList on the events list"
    items = lst["itemListElement"]
    assert event.title in [i["name"] for i in items]
    assert any(f"/events/{event.pk}/saturday-football/" in i["url"] for i in items)


def test_places_list_emits_itemlist():
    _area, _t, place, _event = _fixture()
    html = Client().get("/places/list/").content.decode()
    lst = _node_of_type(html, "ItemList")
    assert lst is not None, "expected an ItemList on the places list"
    items = lst["itemListElement"]
    assert place.display_name in [i["name"] for i in items]
    assert any(f"/places/{place.pk}/" in i["url"] for i in items)


# --- 2. Internal linking ------------------------------------------------------------------


def test_event_detail_links_to_landing():
    area, t, _place, event = _fixture()
    html = Client().get(f"/events/{event.pk}/").content.decode()
    assert f"/things-to-do/{area.slug}/{t.slug}/" in html


def test_place_detail_links_to_landing():
    area, t, place, _event = _fixture()
    html = Client().get(f"/places/{place.pk}/").content.decode()
    # The per-activity landing the venue supports + the city index are both linked.
    assert f"/things-to-do/{area.slug}/{t.slug}/" in html
    assert f"/things-to-do/{area.slug}/" in html


def test_no_landing_link_when_city_has_no_area():
    # A venue/event in a city with no active Area must not render a (404-bound) landing link.
    _area, _t, place, event = _fixture(city="Nowhereville")
    place_html = Client().get(f"/places/{place.pk}/").content.decode()
    event_html = Client().get(f"/events/{event.pk}/").content.decode()
    assert "/things-to-do/" not in place_html
    assert "/things-to-do/" not in event_html


# --- 3. Meta completeness + index hygiene -------------------------------------------------


def test_twitter_card_and_og_locale_present():
    html = Client().get("/").content.decode()
    assert '<meta name="twitter:card" content="summary">' in html
    assert re.search(r'<meta property="og:locale" content="(ro_RO|en_US)">', html)


def test_unfiltered_events_list_is_indexable():
    _fixture()
    html = Client().get("/events/").content.decode()
    assert '<meta name="robots" content="index, follow">' in html


@pytest.mark.parametrize("qs", ["?q=football", "?activity=r4-football", "?area=r4-cluj"])
def test_filtered_events_list_is_noindex(qs):
    _fixture()
    html = Client().get(f"/events/{qs}").content.decode()
    assert '<meta name="robots" content="noindex, follow">' in html


def test_filtered_places_list_is_noindex():
    _fixture()
    assert (
        '<meta name="robots" content="index, follow">'
        in Client().get("/places/list/").content.decode()
    )
    assert (
        '<meta name="robots" content="noindex, follow">'
        in Client().get("/places/list/?city=Cluj-Napoca").content.decode()
    )


# --- Child-safety: the public surfaces never leak the activity layer ----------------------


@pytest.mark.parametrize("path", ["/", "/events/", "/places/list/"])
def test_anonymous_public_surfaces_have_no_activity_links(path):
    _fixture()
    html = Client().get(path).content.decode()
    assert "/activities/" not in html


def test_place_detail_jsonld_only_public_entities():
    # The venue page's JSON-LD must expose only Place/Event/BreadcrumbList — never an activity.
    _area, _t, place, _event = _fixture()
    html = Client().get(f"/places/{place.pk}/").content.decode()
    for block in _ld_blocks(html):
        types = block.get("@type")
        types = types if isinstance(types, list) else [types]
        assert all(t in {"Place", "Event", "BreadcrumbList", "ItemList"} for t in types), types
        for nested in block.get("event", []):
            assert nested["@type"] == "Event"
