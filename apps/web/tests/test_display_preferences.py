"""F12 — display preferences (dark/high-contrast theme, larger text, reduced motion). Pins: the
settings page works signed-out (no login required); a POST persists validated values to functional
cookies + redirects; the context processor stamps the chosen values onto <html> on every page;
defaults are "auto"/"normal"/no-scale; and a tampered/garbage value is ignored (no junk cookie)."""

import pytest
from django.test import Client

from apps.web.context_processors import display_preferences

pytestmark = pytest.mark.django_db


def test_settings_page_is_public_and_lists_options():
    body = Client().get("/display/").content.decode()  # no login
    assert "Display settings" in body
    for value in ("auto", "light", "dark", "contrast", "large", "larger", "reduce", "full"):
        assert f'value="{value}"' in body


def test_post_persists_validated_cookies_and_redirects():
    c = Client()
    resp = c.post(
        "/display/",
        {"display_theme": "dark", "display_text": "large", "display_motion": "reduce"},
    )
    assert resp.status_code == 302
    assert resp.cookies["display_theme"].value == "dark"
    assert resp.cookies["display_text"].value == "large"
    assert resp.cookies["display_motion"].value == "reduce"
    assert resp.cookies["display_theme"]["samesite"] == "Lax"


def test_garbage_value_is_ignored_no_junk_cookie():
    resp = Client().post(
        "/display/",
        {"display_theme": "rainbow", "display_text": "huge", "display_motion": "spin"},
    )
    assert resp.status_code == 302
    # None of the off-allowlist values are written as cookies.
    assert "display_theme" not in resp.cookies
    assert "display_text" not in resp.cookies
    assert "display_motion" not in resp.cookies


def test_chosen_theme_is_stamped_on_html_for_every_page():
    c = Client()
    c.cookies["display_theme"] = "dark"
    c.cookies["display_text"] = "larger"
    c.cookies["display_motion"] = "reduce"
    body = c.get("/display/").content.decode()
    assert 'data-theme="dark"' in body
    assert 'data-motion="reduce"' in body
    assert "--scale: 1.3" in body  # "larger" -> 1.3x rem base


def test_defaults_when_no_cookie(rf):
    request = rf.get("/")
    ctx = display_preferences(request)
    assert ctx == {
        "display_theme": "auto",
        "display_text": "normal",
        "display_motion": "auto",
        "display_scale": "1",
    }


def test_context_processor_rejects_tampered_cookie(rf):
    request = rf.get("/")
    request.COOKIES["display_theme"] = "evil"
    request.COOKIES["display_text"] = "1e9"
    ctx = display_preferences(request)
    assert ctx["display_theme"] == "auto"  # fell back to default
    assert ctx["display_text"] == "normal"
    assert ctx["display_scale"] == "1"


def test_default_page_has_auto_theme():
    body = Client().get("/display/").content.decode()
    assert 'data-theme="auto"' in body
    assert "--scale: 1;" in body
