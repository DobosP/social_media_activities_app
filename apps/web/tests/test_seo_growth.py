"""Round-2 SEO growth levers: URL slugs, city×activity landing pages, event feed, IndexNow.

The recurring assertion is the same child-safety one as round 1: every new public surface routes
through ``public_places()``/``upcoming_events()``, so no ``/activities/`` URL, minor, or pending
venue can leak into a landing page, the feed, or the sitemap.
"""

import json
import re
from datetime import timedelta
from unittest import mock

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


def _area():
    return Area.objects.create(city=CITY, slug="cluj-napoca", name=CITY)


def _type(slug="football"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": slug.title(), "category": cat}
    )
    return t


def _place(name="Central Park"):
    return Place.objects.create(
        name=name,
        location=Point(23.6, 46.77, srid=4326),
        address_city=CITY,
        source=Place.Source.OSM,
    )


def _pending_place():
    return Place.objects.create(
        name="Secret spot",
        location=Point(23.61, 46.78, srid=4326),
        address_city=CITY,
        source=Place.Source.USER,
    )


def _edge(place, activity_type, *, disputed=False):
    return PlaceActivity.objects.create(place=place, activity=activity_type, is_disputed=disputed)


def _event(place, activity_type, *, days=3, title="Saturday football"):
    return Event.objects.create(
        title=title,
        starts_at=timezone.now() + timedelta(days=days),
        place=place,
        activity_type=activity_type,
        source=Event.Source.MANUAL,
    )


def _ld_blocks(html):
    blocks = re.findall(
        r'<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>', html, flags=re.DOTALL
    )
    return [json.loads(b) for b in blocks]


# --- Keyword URL slugs ---------------------------------------------------------------------


def test_bare_place_url_renders_with_slugged_canonical():
    place = _place()
    resp = Client().get(f"/places/{place.pk}/")
    assert resp.status_code == 200
    assert f'rel="canonical" href="http://testserver/places/{place.pk}/central-park/"' in (
        resp.content.decode()
    )


def test_decorative_place_slug_renders_with_same_canonical():
    place = _place()
    # Any (even "wrong") slug renders 200; canonical still points at the keyword-rich path.
    resp = Client().get(f"/places/{place.pk}/whatever/")
    assert resp.status_code == 200
    assert f"/places/{place.pk}/central-park/" in resp.content.decode()


def test_canonical_place_slug_renders_200():
    place = _place()
    assert Client().get(f"/places/{place.pk}/central-park/").status_code == 200


def test_bare_event_url_renders_with_slugged_canonical():
    place, t = _place(), _type()
    event = _event(place, t)
    resp = Client().get(f"/events/{event.pk}/")
    assert resp.status_code == 200
    assert f"/events/{event.pk}/saturday-football/" in resp.content.decode()


def test_pending_place_does_not_redirect_or_leak_name():
    # A pending USER place stays on the bare URL (no slug → no name leak) and 404s anonymously.
    pending = _pending_place()
    resp = Client().get(f"/places/{pending.pk}/")
    assert resp.status_code == 404


# --- Landing pages -------------------------------------------------------------------------


def test_landing_with_supply_lists_public_place_and_event():
    area, t = _area(), _type()
    place = _place()
    _edge(place, t)
    _event(place, t)
    html = Client().get(f"/things-to-do/{area.slug}/{t.slug}/").content.decode()
    assert "Central Park" in html
    assert "Saturday football" in html
    # Breadcrumb JSON-LD present; no activity URL anywhere on the page.
    assert any(b.get("@type") == "BreadcrumbList" for b in _ld_blocks(html))
    assert "/activities/" not in html


def test_landing_without_supply_404s():
    area, t = _area(), _type()
    assert Client().get(f"/things-to-do/{area.slug}/{t.slug}/").status_code == 404


def test_landing_excludes_pending_place_and_its_event():
    area, t = _area(), _type()
    pending = _pending_place()
    _edge(pending, t)
    _event(pending, t, title="Hidden game")
    # No public supply -> 404 (the pending venue and its event must not surface).
    assert Client().get(f"/things-to-do/{area.slug}/{t.slug}/").status_code == 404


def test_landing_index_lists_available_combo():
    area, t = _area(), _type()
    place = _place()
    _edge(place, t)
    html = Client().get("/things-to-do/").content.decode()
    assert area.name in html
    assert f"/things-to-do/{area.slug}/{t.slug}/" in html


# --- Event feed ----------------------------------------------------------------------------


def test_events_feed_lists_upcoming_excludes_past_and_pending():
    place, t = _place(), _type()
    upcoming = _event(place, t, title="Upcoming match")
    past = _event(place, t, days=-5, title="Old match")
    pending_event = _event(_pending_place(), t, title="Pending match")
    resp = Client().get("/events/feed/")
    assert resp.status_code == 200
    assert "application/rss+xml" in resp.headers["Content-Type"]
    body = resp.content.decode()
    assert "Upcoming match" in body
    assert "Old match" not in body
    assert "Pending match" not in body
    # Item link is the canonical slugged event URL.
    assert f"/events/{upcoming.pk}/upcoming-match/" in body
    assert str(past.pk) not in body or "Old match" not in body
    assert pending_event.pk  # referenced


def test_events_feed_activity_filter_narrows():
    place = _place()
    football, chess = _type("football"), _type("chess")
    _event(place, football, title="Football night")
    _event(place, chess, title="Chess night")
    body = Client().get("/events/feed/?activity=football").content.decode()
    assert "Football night" in body
    assert "Chess night" not in body


# --- IndexNow ------------------------------------------------------------------------------


def test_indexnow_key_file_404_when_unconfigured():
    assert Client().get("/indexnow.txt").status_code == 404


@override_settings(INDEXNOW_KEY="abc123key")
def test_indexnow_key_file_serves_key():
    resp = Client().get("/indexnow.txt")
    assert resp.status_code == 200
    assert resp.content.decode() == "abc123key"


def test_submit_urls_is_noop_when_disabled():
    from apps.web import indexnow

    with mock.patch("apps.safety.net.safe_get") as sg:
        assert indexnow.submit_urls(["https://x.test/places/1/a/"]) is False
        sg.assert_not_called()


@override_settings(
    INDEXNOW_ENABLED=True, INDEXNOW_KEY="abc123key", SITE_BASE_URL="https://meet.example.eu"
)
def test_submit_urls_posts_when_enabled():
    from apps.web import indexnow

    with mock.patch("apps.safety.net.safe_get") as sg:
        assert indexnow.submit_urls(["https://meet.example.eu/places/1/central-park/"]) is True
        sg.assert_called_once()
        payload = sg.call_args.kwargs["json"]
        assert payload["host"] == "meet.example.eu"
        assert payload["key"] == "abc123key"
        assert payload["urlList"] == ["https://meet.example.eu/places/1/central-park/"]


@override_settings(
    INDEXNOW_ENABLED=True,
    INDEXNOW_KEY="abc123key",
    SITE_BASE_URL="https://meet.example.eu",
    OPS_HEARTBEAT_URL="https://ping.example/indexnow",
)
def test_submit_urls_pings_heartbeat_summary_when_configured():
    from apps.web import indexnow

    with (
        mock.patch("apps.safety.net.safe_get") as sg,
        mock.patch("apps.ops.heartbeat.ping_heartbeat") as heartbeat,
    ):
        assert indexnow.submit_urls(["https://meet.example.eu/places/1/central-park/"]) is True
        sg.assert_called_once()
        heartbeat.assert_called_once_with({"status": "ok", "submitted": 1, "failed": 0})


@override_settings(
    INDEXNOW_ENABLED=True,
    INDEXNOW_KEY="abc123key",
    SITE_BASE_URL="https://meet.example.eu",
    OPS_HEARTBEAT_URL="https://ping.example/indexnow",
)
def test_submit_urls_pings_failure_summary_when_submit_fails():
    from apps.web import indexnow

    with (
        mock.patch("apps.safety.net.safe_get", side_effect=RuntimeError("network down")),
        mock.patch("apps.ops.heartbeat.ping_heartbeat") as heartbeat,
    ):
        assert indexnow.submit_urls(["https://meet.example.eu/places/1/central-park/"]) is False
        heartbeat.assert_called_once_with({"status": "failed", "submitted": 0, "failed": 1})


@override_settings(
    INDEXNOW_ENABLED=True, INDEXNOW_KEY="abc123key", SITE_BASE_URL="https://meet.example.eu"
)
def test_submit_urls_blocks_a_private_endpoint_before_any_network(monkeypatch):
    # The IndexNow submit goes through the SSRF-safe channel (safety.net.safe_get). If the endpoint
    # were ever (mis)configured to an internal/link-local address, safe_get must reject it BEFORE
    # any outbound I/O — never exfiltrate to e.g. the cloud metadata service.
    import requests

    from apps.web import indexnow

    monkeypatch.setattr(indexnow, "INDEXNOW_ENDPOINT", "http://169.254.169.254/indexnow")
    network_hits = []
    monkeypatch.setattr(requests, "request", lambda *a, **k: network_hits.append((a, k)))
    # submit_urls is best-effort (never raises); a blocked endpoint just yields False.
    assert indexnow.submit_urls(["https://meet.example.eu/places/1/x/"]) is False
    assert network_hits == []  # _validate_host raised UnsafeURLError before requests.request ran


# --- Sitemap regression --------------------------------------------------------------------


def test_sitemap_includes_landing_and_slugged_urls():
    area, t = _area(), _type()
    place = _place()
    _edge(place, t)
    event = _event(place, t)
    body = Client().get("/sitemap.xml").content.decode()
    assert f"/things-to-do/{area.slug}/{t.slug}/" in body
    assert f"/places/{place.pk}/central-park/" in body
    assert f"/events/{event.pk}/saturday-football/" in body
    assert "/activities/" not in body
