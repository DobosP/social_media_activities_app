"""Open-data page + snapshot downloads (schema.org Dataset), event offers/availability
enrichment, and the llms.txt / robots.txt agent-API carve-outs.

Child-safety unchanged: the open-data page and its Dataset JSON-LD link only already-public
surfaces (feeds, the public events/places JSON API, the public discovery endpoints, the
snapshot files apps.web.agent_snapshot writes) — social.Activity is never emitted as JSON-LD
and the blanket ``Disallow: /activities/`` in robots.txt is untouched.
"""

import json
import re
from datetime import timedelta
from decimal import Decimal

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.events.models import Event
from apps.places.models import Place
from apps.web.structured_data import event_ld

pytestmark = pytest.mark.django_db


def _ld_blocks(html):
    blocks = re.findall(
        r'<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>', html, flags=re.DOTALL
    )
    return [json.loads(b) for b in blocks]


def _public_place():
    return Place.objects.create(
        name="Central Park",
        location=Point(23.6, 46.77, srid=4326),
        address_city="Cluj-Napoca",
        source=Place.Source.OSM,
    )


def _event(**kwargs):
    kwargs.setdefault("title", "Saturday football")
    kwargs.setdefault("starts_at", timezone.now() + timedelta(days=3))
    kwargs.setdefault("source", Event.Source.MANUAL)
    return Event.objects.create(**kwargs)


# --- /open-data/ page -------------------------------------------------------------------


def test_open_data_page_200_anonymous_with_dataset_jsonld():
    resp = Client().get("/open-data/")
    assert resp.status_code == 200
    html = resp.content.decode()
    # Nonce'd script tag, matching every other structured-data page.
    assert re.search(r'<script nonce="[^"]+" type="application/ld\+json">', html)
    blocks = _ld_blocks(html)
    datasets = [b for b in blocks if b.get("@type") == "Dataset"]
    assert datasets, "expected a schema.org Dataset block"
    node = datasets[0]
    assert node["url"].endswith("/open-data/")
    assert node["isAccessibleForFree"] is True
    assert node["license"].endswith("/open-data/#licensing")
    assert node["creator"]["name"] == "Activities"
    assert node["spatialCoverage"] == {"@type": "Place", "name": "Cluj-Napoca, Romania"}
    formats = {d["encodingFormat"] for d in node["distribution"]}
    assert {"application/rss+xml", "application/atom+xml", "application/json"} <= formats


def test_open_data_page_escapes_script_breakout():
    # Same seam every other JSON-LD page relies on; the Dataset block has no user-controlled
    # strings today, but this pins the escaping contract at the page level regardless.
    html = Client().get("/open-data/").content.decode()
    assert "</script><b>" not in html


def test_open_data_page_has_no_snapshot_links_when_unconfigured(settings):
    settings.AGENT_SNAPSHOT_DIR = ""
    html = Client().get("/open-data/").content.decode()
    # Neither the human-visible download section nor the Dataset JSON-LD distribution may
    # advertise the snapshot manifest when it would 404 (unconfigured deployment).
    assert '<a href="/open-data/snapshot/manifest.json"' not in html
    datasets = [b for b in _ld_blocks(html) if b.get("@type") == "Dataset"]
    urls = {d.get("contentUrl", "") for d in datasets[0]["distribution"]}
    assert not any(u.endswith("/open-data/snapshot/manifest.json") for u in urls)


def test_dataset_ld_advertises_snapshot_when_exported(tmp_path, settings):
    (tmp_path / "manifest.json").write_text("{}")
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)
    html = Client().get("/open-data/").content.decode()
    datasets = [b for b in _ld_blocks(html) if b.get("@type") == "Dataset"]
    urls = {d.get("contentUrl", "") for d in datasets[0]["distribution"]}
    assert any(u.endswith("/open-data/snapshot/manifest.json") for u in urls)


def test_no_snapshot_links_when_configured_but_never_exported(tmp_path, settings):
    # Configured dir, but the export job has never succeeded (no manifest.json): neither
    # the page links nor the Dataset JSON-LD may advertise downloads that would 404.
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)
    html = Client().get("/open-data/").content.decode()
    assert '<a href="/open-data/snapshot/manifest.json"' not in html
    datasets = [b for b in _ld_blocks(html) if b.get("@type") == "Dataset"]
    urls = {d.get("contentUrl", "") for d in datasets[0]["distribution"]}
    assert not any(u.endswith("/open-data/snapshot/manifest.json") for u in urls)


def test_open_data_page_links_snapshots_when_configured(tmp_path, settings):
    (tmp_path / "manifest.json").write_text("{}")
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)
    html = Client().get("/open-data/").content.decode()
    assert "/open-data/snapshot/manifest.json" in html
    assert "/open-data/snapshot/events.json" in html


# --- sitemap.xml --------------------------------------------------------------------------


def test_sitemap_lists_open_data_page():
    body = Client().get("/sitemap.xml").content.decode()
    assert "/open-data/" in body


# --- snapshot downloads --------------------------------------------------------------------


def test_snapshot_404_when_unconfigured(settings):
    settings.AGENT_SNAPSHOT_DIR = ""
    resp = Client().get("/open-data/snapshot/manifest.json")
    assert resp.status_code == 404


def test_snapshot_404_for_non_whitelisted_name(tmp_path, settings):
    (tmp_path / "manifest.json").write_text("{}")
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)
    # No slash in the segment, so it resolves through <str:name> straight to the whitelist
    # check — no path-traversal surface, no file ever opened.
    resp = Client().get("/open-data/snapshot/../manifest.json")
    assert resp.status_code == 404
    resp = Client().get("/open-data/snapshot/secrets.json")
    assert resp.status_code == 404


def test_snapshot_404_when_file_missing(tmp_path, settings):
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)  # configured, but manifest.json not written
    resp = Client().get("/open-data/snapshot/manifest.json")
    assert resp.status_code == 404


def test_snapshot_200_with_cache_control(tmp_path, settings):
    (tmp_path / "manifest.json").write_text('{"schema_version": 1}')
    settings.AGENT_SNAPSHOT_DIR = str(tmp_path)
    resp = Client().get("/open-data/snapshot/manifest.json")
    assert resp.status_code == 200
    assert resp["Content-Type"] == "application/json"
    assert b"".join(resp.streaming_content) == b'{"schema_version": 1}'
    cc = resp.headers.get("Cache-Control", "")
    assert "public" in cc
    assert not resp.cookies


# --- event_ld: offers / availability enrichment ---------------------------------------------


def test_event_ld_free_event_sets_accessible_for_free():
    place = _public_place()
    event = _event(place=place, source_is_free=True)
    node = event_ld(event)
    assert node["isAccessibleForFree"] is True
    assert "offers" not in node  # no price data at all


def test_event_ld_no_price_data_has_no_offers_key():
    place = _public_place()
    event = _event(place=place)
    node = event_ld(event)
    assert "offers" not in node
    assert "isAccessibleForFree" not in node


def test_event_ld_single_price_emits_offer():
    place = _public_place()
    event = _event(
        place=place,
        source_price_min=Decimal("25.00"),
        source_price_max=Decimal("25.00"),
        source_currency="RON",
        source_availability="available",
    )
    node = event_ld(event)
    assert node["offers"] == {
        "@type": "Offer",
        "price": 25.0,
        "priceCurrency": "RON",
        "availability": "https://schema.org/InStock",
    }


def test_event_ld_price_range_emits_aggregate_offer():
    place = _public_place()
    event = _event(
        place=place,
        source_price_min=Decimal("10.00"),
        source_price_max=Decimal("30.00"),
        source_currency="RON",
    )
    node = event_ld(event)
    assert node["offers"]["@type"] == "AggregateOffer"
    assert node["offers"]["lowPrice"] == 10.0
    assert node["offers"]["highPrice"] == 30.0


def test_event_ld_sold_out_lifecycle_wins_over_source_availability():
    place = _public_place()
    event = _event(
        place=place,
        source_price_min=Decimal("15.00"),
        source_price_max=Decimal("15.00"),
        source_availability="available",  # stale source facet
        lifecycle_status=Event.LifecycleStatus.SOLD_OUT,
    )
    node = event_ld(event)
    assert node["offers"]["availability"] == "https://schema.org/SoldOut"


def test_event_ld_offer_url_from_event_url():
    place = _public_place()
    event = _event(place=place, source_price_min=Decimal("5.00"), url="https://tickets.example/x")
    node = event_ld(event)
    assert node["offers"]["url"] == "https://tickets.example/x"


def test_event_ld_unrecognised_availability_is_omitted():
    place = _public_place()
    event = _event(
        place=place, source_price_min=Decimal("5.00"), source_availability="preorder_only"
    )
    node = event_ld(event)
    assert "availability" not in node["offers"]


# --- llms.txt --------------------------------------------------------------------------------


def test_llms_txt_mentions_open_data_and_events_api():
    body = Client().get("/llms.txt").content.decode()
    assert "/open-data/" in body
    assert "/api/v1/events/" in body
    assert "/api/schema/" in body
    assert "60 requests/minute" in body


# --- robots.txt: agent-API Allow carve-outs ---------------------------------------------------


def test_robots_allows_public_api_in_every_ua_group_but_keeps_child_safety_pin():
    body = Client().get("/robots.txt").content.decode()
    groups = body.split("User-agent: ")[1:]
    assert groups, "expected at least one User-agent group"
    for group in groups:
        assert "Allow: /api/v1/events" in group
        assert "Allow: /api/v1/places" in group
        assert "Allow: /api/schema/" in group
        assert "Disallow: /api/" in group
        assert "Disallow: /activities/" in group  # child-safety pin stays
