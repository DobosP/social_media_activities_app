"""Discoverability surfaces: robots.txt, llms.txt, sitemap.xml, JSON-LD, meta tags.

The load-bearing assertion is the child-safety one: no cohort/activity URL ever reaches a
crawler — robots disallows /activities/ and the sitemap never lists an activity or a pending
(F25) venue. The rest verifies the open-data pages are machine-readable (valid schema.org).
"""

import json
import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client, override_settings
from django.utils import timezone

from apps.events.models import Event
from apps.places.models import Place

pytestmark = pytest.mark.django_db


def _public_place():
    return Place.objects.create(
        name="Central Park",
        location=Point(23.6, 46.77, srid=4326),
        address_city="Cluj-Napoca",
        source=Place.Source.OSM,
    )


def _pending_user_place():
    # A USER place with NO proposal row is correctly hidden by public_places() (F25).
    return Place.objects.create(
        name="Secret spot",
        location=Point(23.61, 46.78, srid=4326),
        source=Place.Source.USER,
    )


def _event(place):
    return Event.objects.create(
        title="Saturday football",
        description="Friendly pickup game, all welcome.",
        starts_at=timezone.now() + timedelta(days=3),
        place=place,
        source=Event.Source.MANUAL,
    )


def _ld_blocks(html):
    """Parse every application/ld+json script block out of a rendered page."""
    blocks = re.findall(
        r'<script type="application/ld\+json">(.*?)</script>', html, flags=re.DOTALL
    )
    return [json.loads(b) for b in blocks]


# --- robots.txt -----------------------------------------------------------------------


def test_robots_welcomes_bots_and_guards_private_paths():
    resp = Client().get("/robots.txt")
    assert resp.status_code == 200
    body = resp.content.decode()
    assert "GPTBot" in body and "ClaudeBot" in body  # AI agents explicitly welcomed
    assert "Disallow: /admin/" in body
    assert "Disallow: /api/" in body
    assert "Disallow: /activities/" in body  # child-safety: cohort meetups never crawled
    assert "Sitemap:" in body


# --- llms.txt -------------------------------------------------------------------------


def test_llms_txt_points_at_public_surfaces():
    resp = Client().get("/llms.txt")
    assert resp.status_code == 200
    body = resp.content.decode()
    assert "/events/" in body
    assert "/places/list/" in body


# --- sitemap.xml ----------------------------------------------------------------------


def test_sitemap_lists_public_place_and_event_only():
    public = _public_place()
    pending = _pending_user_place()
    _event(public)
    body = Client().get("/sitemap.xml").content.decode()
    assert f"/places/{public.pk}/" in body
    # Child-safety + F25 invariants: no activity URLs, no pending venue.
    assert "/activities/" not in body
    assert f"/places/{pending.pk}/" not in body


def test_sitemap_excludes_past_events():
    place = _public_place()
    past = Event.objects.create(
        title="Last week",
        starts_at=timezone.now() - timedelta(days=5),
        place=place,
        source=Event.Source.MANUAL,
    )
    upcoming = _event(place)
    body = Client().get("/sitemap.xml").content.decode()
    assert f"/events/{upcoming.pk}/" in body
    assert f"/events/{past.pk}/" not in body


# --- JSON-LD structured data ----------------------------------------------------------


def test_place_detail_emits_valid_place_jsonld():
    place = _public_place()
    html = Client().get(f"/places/{place.pk}/", follow=True).content.decode()
    blocks = _ld_blocks(html)
    place_nodes = [b for b in blocks if b.get("@type") == "Place"]
    assert place_nodes, "expected a schema.org Place block"
    node = place_nodes[0]
    assert node["name"] == "Central Park"
    assert node["geo"]["latitude"] == pytest.approx(46.77)


def test_event_detail_emits_valid_event_jsonld():
    place = _public_place()
    event = _event(place)
    html = Client().get(f"/events/{event.pk}/", follow=True).content.decode()
    blocks = _ld_blocks(html)
    events = [b for b in blocks if b.get("@type") == "Event"]
    assert events, "expected a schema.org Event block"
    node = events[0]
    assert node["name"] == "Saturday football"
    assert node["startDate"].startswith(str(timezone.now().year))
    assert node["location"]["@type"] == "Place"


def test_pending_place_detail_is_not_public_and_has_no_jsonld():
    pending = _pending_user_place()
    resp = Client().get(f"/places/{pending.pk}/")
    assert resp.status_code == 404  # F25: hidden from the anonymous public


def test_jsonld_escapes_script_breakout():
    # A hostile-looking venue name must not break out of the <script> block.
    place = Place.objects.create(
        name="Bad </script><b>x</b>",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
    )
    html = Client().get(f"/places/{place.pk}/", follow=True).content.decode()
    # The raw closing tag must be escaped inside the JSON-LD; parsing must still succeed.
    assert "</script><b>x</b>" not in html.split("application/ld+json")[1].split("</script>")[0]
    blocks = _ld_blocks(html)
    assert any(b.get("name", "").startswith("Bad ") for b in blocks)


# --- meta tags / canonical ------------------------------------------------------------


def test_home_has_canonical_and_description():
    html = Client().get("/").content.decode()
    assert '<link rel="canonical"' in html
    assert '<meta name="description"' in html
    assert '<meta property="og:site_name"' in html


@override_settings(SITE_BASE_URL="https://meet.example.eu")
def test_canonical_uses_configured_base_url():
    place = _public_place()
    # Bare URL renders at 200 with a canonical <link> to the keyword-rich path (no redirect);
    # follow=True is a harmless no-op here, kept for robustness if that ever changes.
    html = Client().get(f"/places/{place.pk}/", follow=True).content.decode()
    assert (
        f'<link rel="canonical" href="https://meet.example.eu/places/{place.pk}/central-park/"'
        in html
    )
    # The sitemap picks up the same custom domain.
    body = Client().get("/sitemap.xml").content.decode()
    assert "https://meet.example.eu/places/" in body
