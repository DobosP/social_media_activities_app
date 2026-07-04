import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "pw-123-secret"
SCRIPT_RE = re.compile(r"<script\b(?P<attrs>[^>]*)>(?P<body>.*?)</script>", re.I | re.S)
EVENT_HANDLER_RE = re.compile(r"\son[a-zA-Z]+\s*=")
STYLE_ATTR_RE = re.compile(r"\sstyle\s*=", re.I)


def _user(name):
    user = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


def _client(user):
    client = Client()
    client.force_login(user)
    return client


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="csp-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="csp-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="CSP Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="CSP pickup",
        starts_at=timezone.now() + timedelta(days=1),
    )


def _inline_executable_scripts(html):
    for match in SCRIPT_RE.finditer(html):
        attrs = match.group("attrs")
        if re.search(r"\ssrc\s*=", attrs, re.I):
            continue
        script_type = re.search(r'\stype=["\']([^"\']+)["\']', attrs, re.I)
        if script_type and script_type.group(1) in {"application/json", "application/ld+json"}:
            continue
        yield match.group(0)


def test_csp_report_only_policy_has_nonce_ready_script_src_without_inline_script_allowance():
    resp = Client().get("/")
    csp = resp["Content-Security-Policy-Report-Only"]
    assert "script-src 'self' https://unpkg.com" in csp
    assert "'unsafe-inline'" not in csp.split("script-src", 1)[1].split(";", 1)[0]
    assert "'unsafe-inline'" not in csp.split("style-src", 1)[1].split(";", 1)[0]
    assert "report-uri /api/v1/ops/csp-report/" in csp
    assert resp.get("Content-Security-Policy", "") == ""


def test_nonced_json_script_adds_matching_header_nonce_on_messages_page():
    user = _user("csp_msg")
    resp = _client(user).get("/messages/")
    html = resp.content.decode()
    script = re.search(r'<script (?=[^>]*id="mz-config")(?=[^>]*nonce="([^"]+)")', html)
    assert script
    nonce = script.group(1)
    assert f"'nonce-{nonce}'" in resp["Content-Security-Policy-Report-Only"]


def test_key_pages_render_no_inline_executable_scripts_events_or_styles():
    user = _user("csp_pages")
    client = _client(user)
    activity = _activity(user)
    paths = [
        "/",
        "/places/",
        "/activities/",
        "/activities/new/",
        "/messages/",
        "/my-meetups/",
        f"/activities/{activity.pk}/",
    ]
    for path in paths:
        html = client.get(path).content.decode()
        assert list(_inline_executable_scripts(html)) == []
        assert EVENT_HANDLER_RE.search(html) is None
        assert STYLE_ATTR_RE.search(html) is None


def test_structured_json_ld_keeps_nonce_without_inline_executable_script():
    resp = Client().get("/")
    html = resp.content.decode()
    script = re.search(
        r'<script (?=[^>]*type="application/ld\+json")(?=[^>]*nonce="([^"]+)")', html
    )
    assert script
    assert f"'nonce-{script.group(1)}'" in resp["Content-Security-Policy-Report-Only"]
    assert list(_inline_executable_scripts(html)) == []


def test_activity_detail_confirmations_use_data_attributes_not_inline_handlers():
    user = _user("csp_owner")
    activity = _activity(user)
    html = _client(user).get(f"/activities/{activity.pk}/").content.decode()
    assert "data-confirm=" in html
    assert "onsubmit=" not in html
