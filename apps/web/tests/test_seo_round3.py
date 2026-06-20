"""Round-3 SEO: site-verification meta, Organization entity enrichment, ItemList, cache headers.

All env-driven extras are empty by default (no behaviour change until configured); the cache
headers ride only on anonymous open-data endpoints. Child-safety unchanged — the ItemList is
built from ``upcoming_events()`` only.
"""

import json
import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client, override_settings
from django.utils import timezone

from apps.communities.models import Area
from apps.events.models import Event
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

CITY = "Cluj-Napoca"


def _ld_blocks(html):
    blocks = re.findall(
        r'<script type="application/ld\+json">(.*?)</script>', html, flags=re.DOTALL
    )
    return [json.loads(b) for b in blocks]


def _landing_fixture():
    # Slugs unique to this module + get_or_create so it never collides with another test
    # module's taxonomy/area rows in a shared run.
    area, _ = Area.objects.get_or_create(slug="r3-cluj", defaults={"city": CITY, "name": CITY})
    cat, _ = ActivityCategory.objects.get_or_create(slug="r3-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="r3-football", defaults={"name": "Football", "category": cat}
    )
    place = Place.objects.create(
        name="Central Park",
        location=Point(23.6, 46.77, srid=4326),
        address_city=CITY,
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


# --- Verification meta tags ---------------------------------------------------------------


def test_verification_meta_absent_by_default():
    html = Client().get("/").content.decode()
    assert "google-site-verification" not in html
    assert "msvalidate.01" not in html


@override_settings(GOOGLE_SITE_VERIFICATION="g-token-123", BING_SITE_VERIFICATION="b-token-456")
def test_verification_meta_present_when_configured():
    html = Client().get("/").content.decode()
    assert '<meta name="google-site-verification" content="g-token-123">' in html
    assert '<meta name="msvalidate.01" content="b-token-456">' in html


# --- Organization entity enrichment -------------------------------------------------------


def _organization_node(html):
    for block in _ld_blocks(html):
        for node in block.get("@graph", []):
            types = node.get("@type")
            if types == "Organization" or (isinstance(types, list) and "Organization" in types):
                return node
    return None


def test_organization_has_area_served_by_default():
    org = _organization_node(Client().get("/").content.decode())
    assert org is not None
    assert org["areaServed"] == "Cluj-Napoca"
    assert "sameAs" not in org  # empty until configured


@override_settings(
    SITE_SAMEAS=["https://github.com/DobosP/social_media_activities_app"],
    SITE_CONTACT_EMAIL="hello@example.org",
)
def test_organization_sameas_and_email_when_configured():
    org = _organization_node(Client().get("/").content.decode())
    assert org["sameAs"] == ["https://github.com/DobosP/social_media_activities_app"]
    assert org["email"] == "hello@example.org"


# --- Landing ItemList ---------------------------------------------------------------------


def test_landing_emits_itemlist_of_events():
    area, t, _, event = _landing_fixture()
    html = Client().get(f"/things-to-do/{area.slug}/{t.slug}/").content.decode()
    lists = [b for b in _ld_blocks(html) if b.get("@type") == "ItemList"]
    assert lists, "expected an ItemList block"
    names = [i["name"] for i in lists[0]["itemListElement"]]
    assert event.title in names
    # The item URL is the canonical slugged event path.
    assert any(
        f"/events/{event.pk}/saturday-football/" in i["url"] for i in lists[0]["itemListElement"]
    )


# --- Cache-Control headers ----------------------------------------------------------------


@pytest.mark.parametrize("path", ["/robots.txt", "/sitemap.xml", "/llms.txt", "/events/feed/"])
def test_public_seo_endpoints_send_cache_control(path):
    if path == "/sitemap.xml":
        _landing_fixture()  # ensure the sitemap has rows to render
    resp = Client().get(path)
    assert resp.status_code == 200
    assert "public" in resp.headers.get("Cache-Control", "")


def test_landing_page_sends_cache_control():
    area, t, _, _ = _landing_fixture()
    resp = Client().get(f"/things-to-do/{area.slug}/{t.slug}/")
    assert resp.status_code == 200
    assert "public" in resp.headers.get("Cache-Control", "")
